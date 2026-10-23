# Copyright 2026 Matrix Origin
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Table row counts must not depend on the number of scanned columns."""

from unittest.mock import Mock

import pytest
import pytest_asyncio
from sqlalchemy import create_engine, event
from sqlalchemy.ext.asyncio import create_async_engine

from matrixone import AsyncClient, Client
from matrixone.metadata import AsyncMetadataManager, MetadataManager


def install_metadata_result(engine, width):
    @event.listens_for(engine, 'before_cursor_execute', retval=True)
    def metadata_result(connection, cursor, statement, parameters, context, executemany):
        if 'metadata_scan(' not in statement:
            return statement, parameters
        # Two objects contain 2 and 3 rows. Each column repeats its object's count.
        query = ' UNION ALL '.join(
            f"SELECT '{name}' AS object_name, {count} AS rows_cnt, "
            '1 AS null_cnt, 10 AS compress_size, 20 AS origin_size'
            for name, count in [('first', 2), ('second', 3)]
            for _ in range(width)
        )
        return query or 'SELECT NULL AS object_name WHERE 0', ()


@pytest.fixture(params=[0, 1, 2], ids=['empty', 'one-column', 'two-columns'])
def metadata_client(request):
    client = Client(logger=Mock())
    client._engine = create_engine('sqlite:///:memory:')
    try:
        client._metadata = MetadataManager(client)
        install_metadata_result(client._engine, request.param)
        yield client, request.param
    finally:
        client.disconnect()


@pytest_asyncio.fixture(params=[0, 1, 2], ids=['empty', 'one-column', 'two-columns'])
async def async_metadata_client(request):
    client = AsyncClient(logger=Mock())
    client._engine = create_async_engine('sqlite+aiosqlite:///:memory:')
    try:
        client._metadata = AsyncMetadataManager(client)
        install_metadata_result(client._engine.sync_engine, request.param)
        yield client, request.param
    finally:
        await client.disconnect()


def assert_stats(stats, width):
    if not width:
        assert stats == {}
        return
    assert stats['table']['row_cnt'] == 5
    assert stats['table']['total_objects'] == 2
    assert stats['table']['null_cnt'] == 2 * width
    assert stats['table']['original_size'] == f'{40 * width} B'
    assert stats['table']['compress_size'] == f'{20 * width} B'


@pytest.mark.parametrize('use_session', [False, True], ids=['client', 'session'])
def test_row_count_is_independent_of_column_count(metadata_client, use_session):
    client, width = metadata_client
    if use_session:
        with client.session() as session:
            assert_stats(session.metadata.get_table_brief_stats('db', 'table'), width)
    else:
        assert_stats(client.metadata.get_table_brief_stats('db', 'table'), width)


@pytest.mark.asyncio
@pytest.mark.parametrize('use_session', [False, True], ids=['client', 'session'])
async def test_async_row_count_is_independent_of_column_count(async_metadata_client, use_session):
    client, width = async_metadata_client
    if use_session:
        async with client.session() as session:
            assert_stats(await session.metadata.get_table_brief_stats('db', 'table'), width)
    else:
        assert_stats(await client.metadata.get_table_brief_stats('db', 'table'), width)
