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
	"github.com/stretchr/testify/require"
)

// An index key part that names a missing column must fail like MySQL:
// ER_KEY_COLUMN_DOES_NOT_EXIST (1072, 42000), "Key column 'x' doesn't exist in
// table", instead of a generic invalid-input error.
func TestIndexKeyPartMissingColumnReportsKeyColumnError(t *testing.T) {
	for _, tc := range []struct{ name, sql, column string }{
		{"secondary key", "create table t(a int, b int, key(c))", "c"},
		{"named secondary key", "create table t(a int, key k_b (a, Missing))", "Missing"},
		{"unique key", "create table t(a int primary key, unique key uk (col10))", "col10"},
		{"create index", "create index ix on t1(nosuch)", "nosuch"},
		{"create unique index", "create unique index ux on t1(col3)", "col3"},
		{"alter add index", "alter table t1 add index ix (nosuch)", "nosuch"},
		{"alter add unique key", "alter table t1 add unique key ux (nosuch)", "nosuch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildSingleStmt(NewMockOptimizer(false, newPlanTestProcess(t)), t, tc.sql)
			require.Error(t, err)
			var typed *moerr.Error
			require.ErrorAs(t, err, &typed)
			require.Equal(t, moerr.ER_KEY_COLUMN_DOES_NOT_EXIST, typed.MySQLCode(), typed.Error())
			require.Equal(t, "42000", typed.SqlState())
			require.Equal(t, "Key column '"+tc.column+"' doesn't exist in table", typed.Error())
		})
	}
}
