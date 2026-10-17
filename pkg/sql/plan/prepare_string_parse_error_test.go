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

package plan

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/matrixorigin/matrixone/pkg/common/moerr"
    "github.com/matrixorigin/matrixone/pkg/sql/parsers/dialect/mysql"
)

func TestPrepareStringParseErrorMapsOnlySyntaxErrors(t *testing.T) {
    ctx := context.Background()
    for _, sql := range []string{"SELECT FROM", "SELECT * FROM"} {
        _, parseErr := mysql.ParseWithSQLMode(ctx, sql, 1, "")
        if parseErr == nil {
            t.Fatalf("expected invalid SQL to be rejected: %q", sql)
        }
        var positioned mysql.PositionedErr
        if !errors.As(parseErr, &positioned) {
            t.Fatalf("expected PositionedErr for %q, got %T: %v", sql, parseErr, parseErr)
        }
        mapped := prepareStringParseError(ctx, parseErr)
        typed, ok := mapped.(*moerr.Error)
        if !ok {
            t.Fatalf("mapped error for %q is not a MatrixOne SQL error: %T", sql, mapped)
        }
        if typed.MySQLCode() != 1064 || typed.SqlState() != "42000" {
            t.Fatalf("mapped SQL syntax error = %d / %s for %q", typed.MySQLCode(), typed.SqlState(), sql)
        }
        if !strings.Contains(mapped.Error(), parseErr.Error()) {
            t.Fatalf("lost original positioned diagnostic for %q: %v", sql, mapped)
        }
    }
    infra := errors.New("database unavailable")
    if got := prepareStringParseError(ctx, infra); !errors.Is(got, infra) {
        t.Fatalf("infrastructure failure changed: %v", got)
    }
    notSyntax := moerr.NewInvalidInput(ctx, "invalid request")
    if got := prepareStringParseError(ctx, notSyntax); got != notSyntax {
        t.Fatalf("unrelated typed error changed: %v", got)
    }
}
