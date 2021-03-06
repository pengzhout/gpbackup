package helper

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/greenplum-db/gp-common-go-libs/gplog"
	"github.com/greenplum-db/gp-common-go-libs/operating"
	"github.com/greenplum-db/gpbackup/utils"
)

var ( // Shared globals
	content          *int
	dataFile         *string
	oid              *uint
	oidFile          *string
	pipeFile         *string
	printVersion     *bool
	restoreAgent     *bool
	tocFile          *string
	version          string
	pluginConfigFile *string
)

var ( // Globals for restore only
	CleanupGroup  *sync.WaitGroup
	currentPipe   string
	lastPipe      string
	nextPipe      string
	wasTerminated bool
	writer        *bufio.Writer
	writeHandle   *os.File
	errBuf        bytes.Buffer
)

/*
 * Shared functions
 */

func DoHelper() {
	defer DoTeardown()
	InitializeGlobals()
	utils.InitializeSignalHandler(DoCleanup, fmt.Sprintf("restore agent on segment %d", *content), &wasTerminated)
	if *restoreAgent {
		doRestoreAgent()
	} else {
		doBackupHelper()
	}
}

func InitializeGlobals() {
	CleanupGroup = &sync.WaitGroup{}
	CleanupGroup.Add(1)
	content = flag.Int("content", -2, "Content ID of the corresponding segment")
	dataFile = flag.String("data-file", "", "Absolute path to the data file")
	gplog.InitializeLogging("gpbackup_helper", "")
	oid = flag.Uint("oid", 0, "Oid of the table being processed")
	oidFile = flag.String("oid-file", "", "Absolute path to the file containing a list of oids to restore")
	pipeFile = flag.String("pipe-file", "", "Absolute path to the pipe file")
	pluginConfigFile = flag.String("plugin-config", "", "The configuration file to use for a plugin")
	printVersion = flag.Bool("version", false, "Print version number and exit")
	restoreAgent = flag.Bool("restore-agent", false, "Use gpbackup_helper as an agent for restore")
	tocFile = flag.String("toc-file", "", "Absolute path to the table of contents file")
	flag.Parse()
	if *printVersion {
		fmt.Printf("gpbackup_helper %s\n", version)
		os.Exit(0)
	}
	operating.InitializeSystemFunctions()
}

func SetContent(id int) {
	content = &id
}

func SetFilename(name string) {
	tocFile = &name
}

func SetOid(newoid uint) {
	oid = &newoid
}

/*
 * Backup helper functions
 */

func doBackupHelper() {
	toc, lastRead := ReadOrCreateTOC()
	numBytes := ReadAndCountBytes()
	lastProcessed := lastRead + numBytes
	toc.AddSegmentDataEntry(*oid, lastRead, lastProcessed)
	toc.LastByteRead = lastProcessed
	toc.WriteToFile(*tocFile)
}

func ReadOrCreateTOC() (*utils.SegmentTOC, uint64) {
	var toc *utils.SegmentTOC
	var lastRead uint64
	if utils.FileExistsAndIsReadable(*tocFile) {
		toc = utils.NewSegmentTOC(*tocFile)
		lastRead = toc.LastByteRead
	} else {
		toc = &utils.SegmentTOC{}
		toc.DataEntries = make(map[uint]utils.SegmentDataEntry, 1)
		lastRead = 0
	}
	return toc, lastRead
}

func ReadAndCountBytes() uint64 {
	reader := bufio.NewReader(operating.System.Stdin)
	numBytes, _ := io.Copy(operating.System.Stdout, reader)
	return uint64(numBytes)
}

/*
 * Restore helper functions
 */

func getOidListFromFile() []int {
	oidStr, err := operating.System.ReadFile(*oidFile)
	gplog.FatalOnError(err)
	oidStrList := strings.Split(strings.TrimSpace(fmt.Sprintf("%s", oidStr)), "\n")
	oidList := make([]int, len(oidStrList))
	for i, oid := range oidStrList {
		num, _ := strconv.ParseInt(oid, 10, 32)
		oidList[i] = int(num)
	}
	sort.Ints(oidList)
	return oidList
}

func doRestoreAgent() {
	tocEntries := utils.NewSegmentTOC(*tocFile).DataEntries
	lastByte := uint64(0)
	oidList := getOidListFromFile()

	lastPipe = ""
	currentPipe = fmt.Sprintf("%s_%d", *pipeFile, oidList[0])
	nextPipe = ""
	log(fmt.Sprintf("Opening pipe for oid %d", oid))
	writer, writeHandle = getPipeWriter(currentPipe)
	reader := getPipeReader()
	for i, oid := range oidList {
		log(fmt.Sprintf("Restoring table with oid %d", oid))
		if i < len(oidList)-1 {
			nextPipe = fmt.Sprintf("%s_%d", *pipeFile, oidList[i+1])
			createNextPipe()
		} else {
			nextPipe = ""
		}
		start := tocEntries[uint(oid)].StartByte
		end := tocEntries[uint(oid)].EndByte
		log(fmt.Sprintf("Start Byte: %d; End Byte: %d; Last Byte: %d", start, end, lastByte))
		reader.Discard(int(start - lastByte))
		log(fmt.Sprintf("Discarded %d bytes", start-lastByte))
		bytesRead, err := io.CopyN(writer, reader, int64(end-start))
		log(fmt.Sprintf("Read %d bytes", bytesRead))
		gplog.FatalOnError(err, errBuf.String())
		log(fmt.Sprintf("Closing pipe for oid %d", oid))
		flushAndCloseWriter()
		lastByte = end

		lastPipe = currentPipe
		currentPipe = nextPipe
		removeFileIfExists(lastPipe)
		if currentPipe != "" {
			log(fmt.Sprintf("Opening pipe for oid %d", oid))
			writer, writeHandle = getPipeWriter(currentPipe)
		}
	}
}

func createNextPipe() {
	err := syscall.Mkfifo(nextPipe, 0777)
	gplog.FatalOnError(err)
}

func getPipeReader() *bufio.Reader {
	var readHandle io.Reader
	var err error
	if *pluginConfigFile != "" {
		pluginConfig := utils.ReadPluginConfig(*pluginConfigFile)
		cmdStr := fmt.Sprintf("%s restore_data %s %s", pluginConfig.ExecutablePath, pluginConfig.ConfigPath, *dataFile)
		cmd := exec.Command("bash", "-c", cmdStr)

		readHandle, err = cmd.StdoutPipe()
		gplog.FatalOnError(err)
		cmd.Stderr = &errBuf

		err = cmd.Start()
		gplog.FatalOnError(err)

		defer func() {
			if len(errBuf.String()) != 0 {
				gplog.Error(errBuf.String())
			}
		}()
	} else {
		readHandle, err = os.Open(*dataFile)
		gplog.FatalOnError(err)
	}

	var bufIoReader *bufio.Reader
	if strings.HasSuffix(*dataFile, ".gz") {
		gzipReader, err := gzip.NewReader(readHandle)
		gplog.FatalOnError(err)
		bufIoReader = bufio.NewReader(gzipReader)
	} else {
		bufIoReader = bufio.NewReader(readHandle)
	}
	return bufIoReader
}

func getPipeWriter(currentPipe string) (*bufio.Writer, *os.File) {
	fileHandle, err := os.OpenFile(currentPipe, os.O_WRONLY, os.ModeNamedPipe)
	gplog.FatalOnError(err)
	pipeWriter := bufio.NewWriter(fileHandle)
	return pipeWriter, fileHandle
}

func flushAndCloseWriter() {
	if writer != nil {
		err := writer.Flush()
		gplog.FatalOnError(err)
		writer = nil
	}
	if writeHandle != nil {
		err := writeHandle.Close()
		gplog.FatalOnError(err)
		writeHandle = nil
	}
}

func fileExists(filename string) bool {
	_, err := operating.System.Stat(filename)
	return err == nil
}

func removeFileIfExists(filename string) {
	if fileExists(filename) {
		err := os.Remove(filename)
		gplog.FatalOnError(err)
	}
}

/*
 * Shared helper functions
 */

func DoTeardown() {
	recover()
	if wasTerminated {
		CleanupGroup.Wait()
		return
	}
	DoCleanup()
	os.Exit(gplog.GetErrorCode())
}

func DoCleanup() {
	defer func() {
		if err := recover(); err != nil {
			log("Encountered error during cleanup: %v", err)
		}
		log("Cleanup complete")
		CleanupGroup.Done()
	}()
	if wasTerminated {
		/*
		 * If the agent dies during the last table copy, it can still report
		 * success, so we create an error file and check for its presence in
		 * gprestore after the COPYs are finished.
		 */
		handle := utils.MustOpenFileForWriting(fmt.Sprintf("%s_error", *pipeFile))
		handle.Close()
	}
	if *restoreAgent {
		flushAndCloseWriter()
		removeFileIfExists(lastPipe)
		removeFileIfExists(currentPipe)
		removeFileIfExists(nextPipe)
	}
}

func log(s string, v ...interface{}) {
	s = fmt.Sprintf("Segment %d: %s", *content, s)
	gplog.Verbose(s, v...)
}
