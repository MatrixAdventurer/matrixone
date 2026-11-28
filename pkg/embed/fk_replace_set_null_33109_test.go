// Copyright 2026 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package embed

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// #33109 review: REPLACE cascading into an ON DELETE SET NULL grandchild must
// update each grandchild row once even when several cascaded child rows match
// it, and must maintain a composite secondary index on the nulled column.
func TestIssue33109ReplaceCascadeSetNullRuntime(t *testing.T) {
	RunBaseClusterTests(t, func(cluster Cluster) {
		cn, err := cluster.GetCNService(0)
		require.NoError(t, err)
		dsn := fmt.Sprintf("dump:111@tcp(127.0.0.1:%d)/?readTimeout=60s", cn.GetServiceConfig().CN.Frontend.Port)
		db, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		defer db.Close()
		db.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		mustExec := func(query string) {
			t.Helper()
			_, err := db.ExecContext(ctx, query)
			require.NoError(t, err, query)
		}
		mustExec("create database issue_33109_rt")
		defer func() {
			if _, err := db.Exec("drop database issue_33109_rt"); err != nil {
				t.Error(err)
			}
		}()
		mustExec("use issue_33109_rt")

		type gRow struct{ gid, pid, payload sql.NullInt64 }
		scan := func(c *sql.DB, query string, cols int) []gRow {
			t.Helper()
			rows, err := c.QueryContext(ctx, query)
			require.NoError(t, err, query)
			defer rows.Close()
			var out []gRow
			for rows.Next() {
				var r gRow
				if cols == 2 {
					require.NoError(t, rows.Scan(&r.gid, &r.pid))
				} else {
					require.NoError(t, rows.Scan(&r.gid, &r.pid, &r.payload))
				}
				out = append(out, r)
			}
			require.NoError(t, rows.Err())
			return out
		}
		n := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
		null := sql.NullInt64{}

		t.Run("overlapping cascade matches update grandchild once", func(t *testing.T) {
			mustExec("create table op (id int primary key)")
			mustExec("create table oc (id int, seq int, p int, primary key (id, seq), foreign key (p) references op(id) on delete cascade)")
			mustExec("create table og (gid int primary key, pid int, foreign key (pid) references oc(id) on delete set null)")
			mustExec("insert into op values (1), (2)")
			mustExec("insert into oc values (10, 1, 1), (10, 2, 1), (20, 1, 2)")
			mustExec("insert into og values (100, 10), (101, 10), (200, 20)")
			mustExec("replace into op values (1)")

			// Read from an independent connection so a duplicate committed
			// row is visible as a second copy of the primary key.
			other, err := sql.Open("mysql", dsn+"&parseTime=true")
			require.NoError(t, err)
			defer other.Close()
			got := scan(other, "select gid, pid from issue_33109_rt.og order by gid", 2)
			require.Equal(t, []gRow{{gid: n(100), pid: null}, {gid: n(101), pid: null}, {gid: n(200), pid: n(20)}}, got)
		})

		t.Run("composite secondary index on nulled column", func(t *testing.T) {
			mustExec("create table ip (id int primary key)")
			mustExec("create table ic (id int primary key, p int, foreign key (p) references ip(id) on delete cascade)")
			mustExec("create table ig (gid int primary key, pid int, payload int, key ix (pid, payload), foreign key (pid) references ic(id) on delete set null)")
			mustExec("insert into ip values (1), (2)")
			mustExec("insert into ic values (10, 1), (20, 2)")
			mustExec("insert into ig values (100, 10, 7), (200, 20, 7)")
			mustExec("replace into ip values (1)")
			want := []gRow{{gid: n(100), pid: null, payload: n(7)}, {gid: n(200), pid: n(20), payload: n(7)}}
			require.Equal(t, want, scan(db, "select gid, pid, payload from ig order by gid", 3))
			// The hidden index rows must follow the base table.
			require.Equal(t, []gRow{{gid: n(100), pid: null, payload: n(7)}},
				scan(db, "select gid, pid, payload from ig where pid is null and payload = 7", 3))
			require.Equal(t, []gRow{{gid: n(200), pid: n(20), payload: n(7)}},
				scan(db, "select gid, pid, payload from ig where pid = 20 and payload = 7", 3))
			require.Empty(t, scan(db, "select gid, pid, payload from ig where pid = 10 and payload = 7", 3))

			// Read the physical hidden-index rows directly.
			var ixTable string
			require.NoError(t, db.QueryRowContext(ctx, `select index_table_name from mo_catalog.mo_indexes
				where name = 'ix' and table_id = (select rel_id from mo_catalog.mo_tables
				where reldatabase = 'issue_33109_rt' and relname = 'ig') limit 1`).Scan(&ixTable))
			require.Equal(t, want, scan(db, "select __mo_index_pri_col, serial_extract(__mo_index_idx_col, 0 as int), "+
				"serial_extract(__mo_index_idx_col, 1 as int) from `"+ixTable+"` order by __mo_index_pri_col", 3))
		})
	})
}
