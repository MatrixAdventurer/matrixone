"""FulltextFilter must be a SQLAlchemy selectable numeric score.

The SQLite score test exercises SQLAlchemy's result conversion with the
filter's real declared type. It does not emulate MatrixOne MATCH scoring.
"""

import pytest
from sqlalchemy import Column, Integer, MetaData, Table, Text, create_engine, literal, select, type_coerce
from sqlalchemy.dialects import mysql
from sqlalchemy.sql.elements import Label

from matrixone.sqlalchemy_ext.fulltext_search import boolean_match


@pytest.mark.parametrize("score", [0.0, 0.09061906, 0.9, -0.25])
def test_fulltext_projection_keeps_numeric_scores(score):
    match = boolean_match("body").must("needle")
    with create_engine("sqlite://").connect() as conn:
        actual = conn.execute(select(type_coerce(literal(score), match.type))).scalar_one()
    assert type(actual) is float
    assert actual == pytest.approx(score)


def test_fulltext_projection_preserves_sql_null():
    match = boolean_match("body").must("needle")
    with create_engine("sqlite://").connect() as conn:
        assert conn.execute(select(type_coerce(literal(None), match.type))).scalar_one() is None


def test_fulltext_label_is_selectable_and_preserves_legacy_compile():
    docs = Table("docs", MetaData(), Column("id", Integer), Column("body", Text))
    match = boolean_match(docs.c.body).must("needle")
    score = match.label("score")
    assert isinstance(score, Label)
    assert score.compile() == f"{match.compile()} AS score"

    sql = str(select(docs.c.id, score).where(match).compile(dialect=mysql.dialect()))
    assert "MATCH(" in sql
    assert " AS score" in sql
    assert "FROM docs" in sql
    assert "WHERE MATCH(" in sql
    assert " = 1" not in sql


def test_fulltext_label_quotes_alias_in_select():
    score = boolean_match("body").must("needle").label("relevance score")
    sql = str(select(score).compile(dialect=mysql.dialect()))
    assert " AS `relevance score`" in sql
