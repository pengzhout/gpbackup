package backup

import (
	"fmt"

	"github.com/greenplum-db/gp-common-go-libs/cluster"
	"github.com/greenplum-db/gpbackup/utils"
)

/*
 * Functions to run commands on entire cluster during backup
 */

func CreateBackupDirectoriesOnAllHosts() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Creating backup directories", func(contentID int) string {
		return fmt.Sprintf("mkdir -p %s", globalFPInfo.GetDirForContent(contentID))
	}, cluster.ON_SEGMENTS_AND_MASTER)
	globalCluster.CheckClusterError(remoteOutput, "Unable to create backup directories", func(contentID int) string {
		return fmt.Sprintf("Unable to create backup directory %s", globalFPInfo.GetDirForContent(contentID))
	})
}

func CreateSegmentPipesOnAllHostsForBackup() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Creating segment data pipes", func(contentID int) string {
		pipeName := globalFPInfo.GetSegmentPipeFilePath(contentID)
		return fmt.Sprintf("mkfifo %s", pipeName)
	}, cluster.ON_SEGMENTS)
	globalCluster.CheckClusterError(remoteOutput, "Unable to create segment data pipes", func(contentID int) string {
		return "Unable to create segment data pipe"
	})
}

func CleanUpSegmentPipesOnAllHosts() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Cleaning up segment data pipes", func(contentID int) string {
		pipePath := globalFPInfo.GetSegmentPipeFilePath(contentID)
		return fmt.Sprintf("rm -f %s", pipePath)
	}, cluster.ON_SEGMENTS)
	globalCluster.CheckClusterError(remoteOutput, "Unable to clean up segment data pipes", func(contentID int) string {
		return "Unable to clean up segment data pipe"
	})
}

func ReadFromSegmentPipes() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Reading from segment data pipes", func(contentID int) string {
		usingCompression, compressionProgram := utils.GetCompressionParameters()
		pipeFile := globalFPInfo.GetSegmentPipeFilePath(contentID)
		compress := compressionProgram.CompressCommand
		backupFile := globalFPInfo.GetTableBackupFilePath(contentID, 0, true)
		if *pluginConfigFile != "" && usingCompression {
			return fmt.Sprintf("set -o pipefail; nohup tail -n +1 -f %s | %s | %s backup_data %s %s > /dev/null &", pipeFile, compress, pluginConfig.ExecutablePath, pluginConfig.ConfigPath, backupFile)
		} else if *pluginConfigFile != "" && !usingCompression {
			return fmt.Sprintf("set -o pipefail; nohup tail -n +1 -f %s | %s backup_data %s %s > /dev/null &", pipeFile, pluginConfig.ExecutablePath, pluginConfig.ConfigPath, backupFile)
		} else if usingCompression {
			return fmt.Sprintf("set -o pipefail; nohup tail -n +1 -f %s | %s > %s &", pipeFile, compress, backupFile)
		}
		return fmt.Sprintf("nohup tail -n +1 -f %s > %s &", pipeFile, backupFile)
	}, cluster.ON_SEGMENTS)
	globalCluster.CheckClusterError(remoteOutput, "Unable to read from segment data pipes", func(contentID int) string {
		return "Unable to read from segment data pipe"
	})
}

func CleanUpSegmentTailProcesses() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Cleaning up segment tail processes", func(contentID int) string {
		filePattern := fmt.Sprintf("gpbackup_%d_%s", contentID, globalFPInfo.Timestamp) // Matches pipe name for backup and file name for restore
		/*
		 * We try to avoid erroring out if no tail processes are found, as this
		 * function is called in DoCleanup and it's possible no tail processes
		 * were started yet if cleanup occurs due to an interrupt.
		 */
		return fmt.Sprintf("PIDS=`ps ux | grep tail | grep \"%s\" | grep -v grep | awk '{print $2}'`; if [[ ! -z \"$PIDS\" ]]; then kill -9 $PIDS; fi", filePattern)
	}, cluster.ON_SEGMENTS)
	globalCluster.CheckClusterError(remoteOutput, "Unable to clean up tail processes", func(contentID int) string {
		return "Unable to clean up tail process"
	})
}

func MoveSegmentTOCsAndMakeReadOnly() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Setting permissions on segment table of contents files and moving to backup directories", func(contentID int) string {
		tocFile := globalFPInfo.GetSegmentTOCFilePath(globalCluster.SegDirMap[contentID], fmt.Sprintf("%d", contentID))
		return fmt.Sprintf("chmod 444 %s; mv %s %s/.", tocFile, tocFile, globalFPInfo.GetDirForContent(contentID))
	}, cluster.ON_SEGMENTS)
	globalCluster.CheckClusterError(remoteOutput, "Unable to set permissions on or move segment table of contents files", func(contentID int) string {
		return fmt.Sprintf("Unable to set permissions on or move file %s", globalFPInfo.GetSegmentTOCFilePath(globalCluster.SegDirMap[contentID], fmt.Sprintf("%d", contentID)))
	})
}
func CleanUpSegmentTOCs() {
	remoteOutput := globalCluster.GenerateAndExecuteCommand("Cleaning up segment table of contents files", func(contentID int) string {
		tocFile := globalFPInfo.GetSegmentTOCFilePath(globalCluster.SegDirMap[contentID], fmt.Sprintf("%d", contentID))
		return fmt.Sprintf("rm -f %s", tocFile)
	}, cluster.ON_SEGMENTS)
	globalCluster.CheckClusterError(remoteOutput, "Unable to remove segment table of contents files", func(contentID int) string {
		return fmt.Sprintf("Unable to remove segment table of contents file %s", globalFPInfo.GetSegmentTOCFilePath(globalCluster.SegDirMap[contentID], fmt.Sprintf("%d", contentID)))
	})
}
