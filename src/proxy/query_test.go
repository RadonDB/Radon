/*
 * Radon
 *
 * Copyright 2018 The Radon Authors.
 * Code is licensed under the GPLv3.
 *
 */

package proxy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xelabs/go-mysqlstack/driver"
	querypb "github.com/xelabs/go-mysqlstack/sqlparser/depends/query"
	"github.com/xelabs/go-mysqlstack/sqlparser/depends/sqltypes"
	"github.com/xelabs/go-mysqlstack/xlog"
)

func TestProxyQueryTxn(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()
	address := proxy.Address()
	querys := []string{
		"start transaction",
		"commit",
		"SET autocommit=0",
	}

	// fakedbs.
	{
		fakedbs.AddQueryPattern("use .*", &sqltypes.Result{})
		fakedbs.AddQueryPattern("XA .*", result1)
		for _, query := range querys {
			fakedbs.AddQueryPattern(query, &sqltypes.Result{})
		}
	}

	proxy.SetTwoPC(true)
	{
		client, err := driver.NewConn("mock", "mock", address, "test", "utf8")
		assert.Nil(t, err)
		defer client.Close()

		for _, query := range querys {
			_, err = client.FetchAll(query, -1)
			assert.Nil(t, err)
		}
	}
}

func TestProxyQuerySet(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()
	address := proxy.Address()
	querys := []string{
		"SET autocommit=0",
		"SET SESSION wait_timeout = 2147483",
		"SET NAMES utf8",
	}

	// fakedbs.
	{
		fakedbs.AddQueryPattern("use .*", &sqltypes.Result{})
		for _, query := range querys {
			fakedbs.AddQueryPattern(query, &sqltypes.Result{})
		}
	}

	{
		client, err := driver.NewConn("mock", "mock", address, "test", "utf8")
		assert.Nil(t, err)
		defer client.Close()

		// Support.
		for _, query := range querys {
			_, err = client.FetchAll(query, -1)
			assert.Nil(t, err)
		}
	}
}

// JDBC/Pthon connector tests.
func TestProxyQueryDriver(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()
	address := proxy.Address()
	querys := []string{
		"/*!40014 SET FOREIGN_KEY_CHECKS=0*/",
		"select a /*xx*/ from t1",
		"SET NAMES 'utf8' COLLATE 'utf8_general_ci'",
	}

	// fakedbs.
	{
		fakedbs.AddQueryPattern("use .*", &sqltypes.Result{})
		fakedbs.AddQueryPattern("select .*", &sqltypes.Result{})
		fakedbs.AddQueryPattern("create .*", &sqltypes.Result{})
		for _, query := range querys {
			fakedbs.AddQuery(query, &sqltypes.Result{})
		}
	}

	// create database.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create database test"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// create test table.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create table test.t1(id int, b int) partition by hash(id)"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	{
		client, err := driver.NewConn("mock", "mock", address, "test", "utf8")
		assert.Nil(t, err)
		defer client.Close()

		// Support.
		for _, query := range querys {
			_, err = client.FetchAll(query, -1)
			assert.Nil(t, err)
		}
	}
}

// Proxy with query.
func TestProxyQuerys(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()
	address := proxy.Address()

	result11 := &sqltypes.Result{
		Fields: []*querypb.Field{
			{
				Name: "id",
				Type: querypb.Type_INT32,
			},
			{
				Name: "name",
				Type: querypb.Type_VARCHAR,
			},
		},
		Rows: make([][]sqltypes.Value, 0, 256)}

	for i := 0; i < 2017; i++ {
		row := []sqltypes.Value{
			sqltypes.MakeTrusted(querypb.Type_INT32, []byte("11")),
			sqltypes.MakeTrusted(querypb.Type_VARCHAR, []byte("1nice name")),
		}
		result11.Rows = append(result11.Rows, row)
	}

	// fakedbs.
	{
		fakedbs.AddQueryPattern("create .*", &sqltypes.Result{})
		fakedbs.AddQueryPattern("select .*", result11)
	}

	// create database.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create database test"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// create test table.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create table test.t1(id int, b int) partition by hash(id)"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// select.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		{
			query := "select  * from test.t1"
			qr, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			want := 60510
			got := int(qr.RowsAffected)
			assert.Equal(t, want, got)
		}
		{ // select * from test.t1 t1 as ...;
			query := "select * from test.t1 as aliaseTable"
			qr, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			want := 60510
			got := int(qr.RowsAffected)
			assert.Equal(t, want, got)
		}
		{ // select id from t1 as ...;
			query := "set @@SESSION.radon_streaming_fetch='ON'"
			_, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			query = "select * from test.t1 as aliaseTable"
			qr, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			want := 60510
			got := int(qr.RowsAffected)
			assert.Equal(t, want, got)
			query = "set @@SESSION.radon_streaming_fetch='OFF'"
			_, err = client.FetchAll(query, -1)
			assert.Nil(t, err)
		}
		{ // select 1 from dual
			query := "select 1 from dual"
			qr, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			want := 2017
			got := int(qr.RowsAffected)
			assert.Equal(t, want, got)
		}
		{ // select 1
			query := "select 1"
			qr, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			want := 2017
			got := int(qr.RowsAffected)
			assert.Equal(t, want, got)
		}
		{ // select @@version_comment limit 1 [from] [dual]
			query := "select @@version_comment limit 1"
			qr, err := client.FetchAll(query, -1)
			assert.Nil(t, err)
			want := 2017
			got := int(qr.RowsAffected)
			assert.Equal(t, want, got)
		}
	}

	// select * from systemdatabase.table
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "select * from information_schema.SCHEMATA"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// select .* from dual  error
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		fakedbs.ResetAll()
		fakedbs.AddQueryErrorPattern("select .*", errors.New("mock.mysql.select.from.dual.error"))
		{ // ERROR 1054 (42S22): Unknown column 'a' in 'field list'
			query := "select a from dual"
			_, err := client.FetchAll(query, -1)
			want := "mock.mysql.select.from.dual.error (errno 1105) (sqlstate HY000)"
			got := err.Error()
			assert.Equal(t, want, got)
		}
		{
			query := "set @@SESSION.radon_streaming_fetch='ON'"
			_, err := client.FetchAll(query, -1)
			assert.Nil(t, err)

			query = "select a from test.dual"
			_, err = client.FetchAll(query, -1)
			want := "Table 'dual' doesn't exist (errno 1146) (sqlstate 42S02)"
			got := err.Error()
			assert.Equal(t, want, got)

			query = "set @@SESSION.radon_streaming_fetch='OFF'"
			_, err = client.FetchAll(query, -1)
			assert.Nil(t, err)
		}
	}
}

func TestProxyQueryStmtPrepare(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()
	address := proxy.Address()

	result11 := &sqltypes.Result{
		Fields: []*querypb.Field{
			{
				Name: "id",
				Type: querypb.Type_INT32,
			},
			{
				Name: "name",
				Type: querypb.Type_VARCHAR,
			},
		},
		Rows: [][]sqltypes.Value{
			{
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("10")),
				sqltypes.MakeTrusted(sqltypes.VarChar, []byte("name1")),
			},
		},
	}

	// fakedbs.
	{
		fakedbs.AddQueryPattern("create .*", &sqltypes.Result{})
		fakedbs.AddQueryPattern("use .*", &sqltypes.Result{})
		fakedbs.AddQuery("insert into test.t1_0021(id, name) values (10, 'name1')", result11)
		fakedbs.AddQuery("select * from test.t1_0021 as t1 where id = 10 and name = 'name1'", result11)
	}

	// create database.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create database test"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// create test table.
	{
		client, err := driver.NewConn("mock", "mock", address, "test", "utf8")
		assert.Nil(t, err)
		query := "create table test.t1(id int, b int) partition by hash(id)"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// prepare.
	{
		client, err := driver.NewConn("mock", "mock", address, "test", "utf8")
		assert.Nil(t, err)

		// Insert.
		{
			params := []sqltypes.Value{
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("10")),
				sqltypes.MakeTrusted(sqltypes.VarChar, []byte("name1")),
			}

			query := "insert into t1(id, name) values(?,?)"
			stmt, err := client.ComStatementPrepare(query)
			assert.Nil(t, err)

			err = stmt.ComStatementExecute(params)
			assert.Nil(t, err)
			stmt.ComStatementClose()
		}

		// Select.
		{
			params := []sqltypes.Value{
				sqltypes.MakeTrusted(sqltypes.Int32, []byte("10")),
				sqltypes.MakeTrusted(sqltypes.VarChar, []byte("name1")),
			}
			query := "select * from t1 where id=? and name=?"

			stmt, err := client.ComStatementPrepare(query)
			assert.Nil(t, err)

			qr, err := stmt.ComStatementQuery(params)
			assert.Nil(t, err)
			log.Debug("%+v", qr)
			stmt.ComStatementClose()
		}
	}
}

// Proxy with system database query.
// Such as: select * from information_schema.
func TestProxyQuerySystemDatabase(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()
	address := proxy.Address()

	// fakedbs.
	{
		fakedbs.AddQueryPattern("create .*", &sqltypes.Result{})
		fakedbs.AddQueryPattern("select .*", &sqltypes.Result{})
	}

	// create database.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create database test"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// create test table.
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "create table test.t1(id int, b int) partition by hash(id)"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// select * from mysql.user
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "select * from mysql.user"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// select * from systemdatabase.table
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "select * from information_schema.SCHEMATA"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// select * from information_schema.COLUMNS where TABLE_NAME='t1' and TABLE_SCHEMA='test'
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "select * from information_schema.COLUMNS where TABLE_NAME='t1' and TABLE_SCHEMA='test'"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// ClickHouse MySQL Driver:
	// SELECT COLUMN_NAME AS name, DATA_TYPE AS type, IS_NULLABLE = 'YES' AS is_nullable, COLUMN_TYPE LIKE '%unsigned%' AS is_unsigned, CHARACTER_MAXIMUM_LENGTH AS length FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='test' AND TABLE_NAME='t1' ORDER BY ORDINAL_POSITION
	// rewrite to:
	// select COLUMN_NAME as name, DATA_TYPE as type, IS_NULLABLE = 'YES' as is_nullable, COLUMN_TYPE like '%unsigned%' as is_unsigned, CHARACTER_MAXIMUM_LENGTH as length from INFORMATION_SCHEMA.`columns` where TABLE_SCHEMA = 'test' and TABLE_NAME = 't1_0000' order by ORDINAL_POSITION asc
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		query := "SELECT COLUMN_NAME AS name, DATA_TYPE AS type, IS_NULLABLE = 'YES' AS is_nullable, COLUMN_TYPE LIKE '%unsigned%' AS is_unsigned, CHARACTER_MAXIMUM_LENGTH AS length FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA='test' AND TABLE_NAME='t1' ORDER BY ORDINAL_POSITION"
		_, err = client.FetchAll(query, -1)
		assert.Nil(t, err)
	}

	// error
	{
		client, err := driver.NewConn("mock", "mock", address, "", "utf8")
		assert.Nil(t, err)
		fakedbs.AddQueryErrorPattern("select .*", errors.New("mysql.select.from.information_schema.error"))
		query1 := "select * from information_schema.SCHEMATA"
		_, err = client.FetchAll(query1, -1)
		assert.NotNil(t, err)
		want := "mysql.select.from.information_schema.error (errno 1105) (sqlstate HY000)"
		got := err.Error()
		assert.Equal(t, want, got)
	}
}

// Test with long query time
func TestLongQuery(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()

	// set longQueryTime = 0s
	proxy.SetLongQueryTime(0)
	address := proxy.Address()
	client, err := driver.NewConn("mock", "mock", address, "", "utf8")
	assert.Nil(t, err)
	defer client.Close()

	querys := []string{
		"select 1 from dual",
	}
	querysError := []string{
		"select a a from dual",
	}

	// fakedbs: add a query and returns the expected result without no delay
	{
		fakedbs.AddQueryPattern("select 1 from dual", &sqltypes.Result{})
	}

	{
		// long query success
		{
			for _, query := range querys {
				_, err = client.FetchAll(query, -1)
				assert.Nil(t, err)
			}
		}
		// long query failed
		{
			for _, query := range querysError {
				_, err = client.FetchAll(query, -1)
				assert.NotNil(t, err)
			}
		}
	}
}

// Test with long query time
func TestLongQuery2(t *testing.T) {
	log := xlog.NewStdLog(xlog.Level(xlog.PANIC))
	fakedbs, proxy, cleanup := MockProxy(log)
	defer cleanup()

	// set longQueryTime = 5s
	proxy.SetLongQueryTime(5)
	address := proxy.Address()
	client, err := driver.NewConn("mock", "mock", address, "", "utf8")
	assert.Nil(t, err)
	defer client.Close()

	querys := []string{
		"select 1 from dual",
	}
	querysError := []string{
		"select a a from dual",
	}
	// fakedbs: add a query and returns the expected result returned by delay 6s
	{
		fakedbs.AddQueryDelay("select 1 from dual", &sqltypes.Result{}, 6*1000)
	}

	{
		// long query success
		{
			for _, query := range querys {
				_, err = client.FetchAll(query, -1)
				assert.Nil(t, err)
			}
		}
		// long query failed
		{
			for _, query := range querysError {
				_, err = client.FetchAll(query, -1)
				assert.NotNil(t, err)
			}
		}
	}
}
