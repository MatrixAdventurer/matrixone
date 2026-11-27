// Copyright 2026 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plan

import (
	"testing"

	"github.com/matrixorigin/matrixone/pkg/common/moerr"
	"github.com/matrixorigin/matrixone/pkg/container/types"
	"github.com/stretchr/testify/require"
)

// CHAR/BINARY columns wider than 255 must fail like MySQL: ER_TOO_BIG_FIELDLENGTH
// (1074, 42000) naming the declared column, not a generic out-of-range error.
func TestDDLCharWidthTooBig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sql     string
		column  string
		wantErr bool
		wantOID types.T
	}{
		{name: "char above max", sql: "create table t(code char(256))", column: "code", wantErr: true},
		{name: "binary above max", sql: "create table t(digest binary(300))", column: "digest", wantErr: true},
		{name: "upper case keyword", sql: "CREATE TABLE t(Code CHAR(1000))", column: "Code", wantErr: true},
		{name: "second column", sql: "create table t(a int, tag char(256) not null)", column: "tag", wantErr: true},
		{name: "char boundary", sql: "create table t(code char(255))", column: "code", wantOID: types.T_char},
		{name: "binary boundary", sql: "create table t(digest binary(255))", column: "digest", wantOID: types.T_binary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built, err := buildSingleStmt(NewMockOptimizer(false, newPlanTestProcess(t)), t, tc.sql)
			if tc.wantErr {
				require.Error(t, err)
				var typed *moerr.Error
				require.ErrorAs(t, err, &typed)
				require.Equal(t, moerr.ER_TOO_BIG_FIELDLENGTH, typed.MySQLCode())
				require.Equal(t, "42000", typed.SqlState())
				require.Equal(t,
					"Column length too big for column '"+tc.column+"' (max = 255); use BLOB or TEXT instead",
					typed.Error())
				return
			}
			require.NoError(t, err)
			col := FindColumn(built.GetDdl().GetCreateTable().TableDef.Cols, tc.column)
			require.NotNil(t, col)
			require.Equal(t, int32(tc.wantOID), col.Typ.Id)
			require.Equal(t, int32(255), col.Typ.Width)
		})
	}
}

func TestDDLCharWidthTooBigAlter(t *testing.T) {
	for _, tc := range []struct{ sql, column string }{
		{"alter table t1 add column note char(256)", "note"},
		{"alter table t1 modify column b char(256)", "b"},
		{"alter table t1 change column b digest binary(256)", "digest"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			_, err := buildSingleStmt(NewMockOptimizer(false, newPlanTestProcess(t)), t, tc.sql)
			require.Error(t, err)
			var typed *moerr.Error
			require.ErrorAs(t, err, &typed)
			require.Equal(t, moerr.ER_TOO_BIG_FIELDLENGTH, typed.MySQLCode())
			require.Contains(t, typed.Error(), "'"+tc.column+"' (max = 255)")
		})
	}
}
