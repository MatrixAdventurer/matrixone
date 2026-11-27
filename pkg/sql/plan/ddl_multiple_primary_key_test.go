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

// CREATE TABLE with a second primary key must fail like MySQL and like the
// ALTER TABLE paths already do: ER_MULTIPLE_PRI_KEY (1068, 42000).
func TestCreateTableMultiplePrimaryKeyReports1068(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"two column attributes", "create table t(a int primary key, b int primary key)"},
		{"attribute then table constraint", "create table t(a int primary key, b int, primary key (b))"},
		{"same column twice", "create table t(a int primary key, primary key (a))"},
		{"two table constraints", "create table t(a int, b int, primary key (a), primary key (b))"},
		{"table constraint then attribute", "create table t(a int, primary key (a), b int primary key)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildSingleStmt(NewMockOptimizer(false, newPlanTestProcess(t)), t, tc.sql)
			require.Error(t, err)
			var typed *moerr.Error
			require.ErrorAs(t, err, &typed)
			require.Equal(t, moerr.ER_MULTIPLE_PRI_KEY, typed.MySQLCode(), typed.Error())
			require.Equal(t, "42000", typed.SqlState())
			require.Equal(t, "Multiple primary key defined", typed.Error())
		})
	}
}

func TestCreateTableSinglePrimaryKeyStillBuilds(t *testing.T) {
	for _, sql := range []string{
		"create table t(a int primary key, b int)",
		"create table t(a int, b int, primary key (a, b))",
	} {
		_, err := buildSingleStmt(NewMockOptimizer(false, newPlanTestProcess(t)), t, sql)
		require.NoError(t, err, sql)
	}
}
