package backup_test

import (
	"database/sql/driver"
	"regexp"

	"github.com/greenplum-db/gp-common-go-libs/structmatcher"
	"github.com/greenplum-db/gpbackup/backup"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"gopkg.in/DATA-DOG/go-sqlmock.v1"
)

var _ = Describe("backup/queries_shared tests", func() {
	Describe("GetMetadataForObjectType", func() {
		var params backup.MetadataQueryParams
		header := []string{"oid", "privileges", "owner", "comment"}
		emptyRows := sqlmock.NewRows(header)

		BeforeEach(func() {
			params = backup.MetadataQueryParams{NameField: "name", OwnerField: "owner", CatalogTable: "table"}
		})
		It("queries metadata for an object with default params", func() {
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid,
	'' AS privileges,
	'' AS kind,
	pg_get_userbyid(owner) AS owner,
	coalesce(description,'') AS comment
FROM table o LEFT JOIN pg_description d ON (d.objoid = o.oid AND d.classoid = 'table'::regclass AND d.objsubid = 0)
ORDER BY o.oid;`)).WillReturnRows(emptyRows)
			backup.GetMetadataForObjectType(connection, params)
		})
		It("queries metadata for an object with a schema field", func() {
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid,
	'' AS privileges,
	'' AS kind,
	pg_get_userbyid(owner) AS owner,
	coalesce(description,'') AS comment
FROM table o LEFT JOIN pg_description d ON (d.objoid = o.oid AND d.classoid = 'table'::regclass AND d.objsubid = 0)
JOIN pg_namespace n ON o.schema = n.oid
WHERE n.nspname NOT LIKE 'pg_temp_%' AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT IN ('gp_toolkit', 'information_schema', 'pg_aoseg', 'pg_bitmapindex', 'pg_catalog')
ORDER BY o.oid;`)).WillReturnRows(emptyRows)
			params.SchemaField = "schema"
			backup.GetMetadataForObjectType(connection, params)
		})
		It("queries metadata for an object with an ACL field", func() {
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid,
	CASE
		WHEN acl IS NULL OR array_upper(acl, 1) = 0 THEN acl[0]
		ELSE unnest(acl)
		END AS privileges,
	CASE
		WHEN acl IS NULL THEN 'Default'
		WHEN array_upper(acl, 1) = 0 THEN 'Empty'
		ELSE '' END AS kind,
	pg_get_userbyid(owner) AS owner,
	coalesce(description,'') AS comment
FROM table o LEFT JOIN pg_description d ON (d.objoid = o.oid AND d.classoid = 'table'::regclass AND d.objsubid = 0)

ORDER BY o.oid;`)).WillReturnRows(emptyRows)
			params.ACLField = "acl"
			backup.GetMetadataForObjectType(connection, params)
		})
		It("queries metadata for a shared object", func() {
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid,
	'' AS privileges,
	'' AS kind,
	pg_get_userbyid(owner) AS owner,
	coalesce(description,'') AS comment
FROM table o LEFT JOIN pg_shdescription d ON (d.objoid = o.oid AND d.classoid = 'table'::regclass)

ORDER BY o.oid;`)).WillReturnRows(emptyRows)
			params.Shared = true
			backup.GetMetadataForObjectType(connection, params)
		})
		It("returns metadata for multiple objects", func() {
			aclRowOne := []driver.Value{"1", "gpadmin=a/gpadmin", "testrole", ""}
			aclRowTwo := []driver.Value{"1", "testrole=a/gpadmin", "testrole", ""}
			commentRow := []driver.Value{"2", "", "testrole", "This is a metadata comment."}
			fakeRows := sqlmock.NewRows(header).AddRow(aclRowOne...).AddRow(aclRowTwo...).AddRow(commentRow...)
			mock.ExpectQuery(`SELECT (.*)`).WillReturnRows(fakeRows)
			params.ACLField = "acl"
			resultMetadataMap := backup.GetMetadataForObjectType(connection, params)

			expectedOne := backup.ObjectMetadata{Privileges: []backup.ACL{
				{Grantee: "gpadmin", Insert: true},
				{Grantee: "testrole", Insert: true},
			}, Owner: "testrole"}
			expectedTwo := backup.ObjectMetadata{Privileges: []backup.ACL{}, Owner: "testrole", Comment: "This is a metadata comment."}
			resultOne := resultMetadataMap[1]
			resultTwo := resultMetadataMap[2]
			Expect(len(resultMetadataMap)).To(Equal(2))
			structmatcher.ExpectStructsToMatch(&expectedOne, &resultOne)
			structmatcher.ExpectStructsToMatch(&expectedTwo, &resultTwo)
		})
	})
	Describe("GetCommentsForObjectType", func() {
		var params backup.MetadataQueryParams
		header := []string{"oid", "comment"}
		emptyRows := sqlmock.NewRows(header)

		BeforeEach(func() {
			params = backup.MetadataQueryParams{NameField: "name", OidField: "oid", CatalogTable: "table"}
		})
		It("returns comment for object with default params", func() {
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid AS oid,
	coalesce(description,'') AS comment
FROM table o JOIN pg_description d ON (d.objoid = oid AND d.classoid = 'table'::regclass AND d.objsubid = 0);`)).WillReturnRows(emptyRows)
			backup.GetCommentsForObjectType(connection, params)
		})
		It("returns comment for object with different comment table", func() {
			params.CommentTable = "comment_table"
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid AS oid,
	coalesce(description,'') AS comment
FROM table o JOIN pg_description d ON (d.objoid = oid AND d.classoid = 'comment_table'::regclass AND d.objsubid = 0);`)).WillReturnRows(emptyRows)
			backup.GetCommentsForObjectType(connection, params)
		})
		It("returns comment for a shared object", func() {
			params.Shared = true
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT
	o.oid AS oid,
	coalesce(description,'') AS comment
FROM table o JOIN pg_shdescription d ON (d.objoid = oid AND d.classoid = 'table'::regclass);`)).WillReturnRows(emptyRows)
			backup.GetCommentsForObjectType(connection, params)
		})
		It("returns comments for multiple objects", func() {
			rowOne := []driver.Value{"1", "This is a metadata comment."}
			rowTwo := []driver.Value{"2", "This is also a metadata comment."}
			fakeRows := sqlmock.NewRows(header).AddRow(rowOne...).AddRow(rowTwo...)
			mock.ExpectQuery(`SELECT (.*)`).WillReturnRows(fakeRows)
			resultMetadataMap := backup.GetCommentsForObjectType(connection, params)

			expectedOne := backup.ObjectMetadata{Privileges: []backup.ACL{}, Comment: "This is a metadata comment."}
			expectedTwo := backup.ObjectMetadata{Privileges: []backup.ACL{}, Comment: "This is also a metadata comment."}
			resultOne := resultMetadataMap[1]
			resultTwo := resultMetadataMap[2]
			Expect(len(resultMetadataMap)).To(Equal(2))
			structmatcher.ExpectStructsToMatch(&expectedOne, &resultOne)
			structmatcher.ExpectStructsToMatch(&expectedTwo, &resultTwo)
		})
	})
})
