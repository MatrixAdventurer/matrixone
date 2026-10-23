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

"""Verify SDK row statistics against COUNT(*) through a real frontend."""

from uuid import uuid4

import pytest

from matrixone import AsyncClient, Client
from matrixone.config import get_connection_kwargs


@pytest.fixture(scope='module')
def metadata_table():
    config = get_connection_kwargs(database='mo_catalog')
    config = {key: config[key] for key in ('host', 'port', 'user', 'password', 'database')}
    client = Client()
    try:
        client.connect(**config)
        database = 'sdk_row_count_' + uuid4().hex
        client.execute(f'CREATE DATABASE {database}')
        try:
            client.execute(f'CREATE TABLE {database}.sample (first INT, second INT)')
            client.execute(f'INSERT INTO {database}.sample VALUES (1, 10), (2, 20)')
            client.moctl.flush_table(database, 'sample')
            yield client, database, config
        finally:
            client.execute(f'DROP DATABASE {database}')
            assert database not in [row[0] for row in client.execute('SHOW DATABASES').fetchall()]
    finally:
        client.disconnect()


def assert_stats(stats, expected, raw):
    assert expected == 2
    assert {row.col_name for row in raw} == {'first', 'second'}
    assert sum(row.rows_cnt for row in raw) == expected * 2
    assert stats['sample']['row_cnt'] == expected
    assert stats['sample']['total_objects'] == len({row.object_name for row in raw})


@pytest.mark.parametrize('use_session', [False, True], ids=['client', 'session'])
def test_real_frontend_row_count_sync(metadata_table, use_session):
    client, database, _ = metadata_table

    def check(executor):
        expected = executor.execute(f'SELECT COUNT(*) FROM {database}.sample').scalar()
        raw = executor.metadata.scan(database, 'sample').fetchall()
        stats = executor.metadata.get_table_brief_stats(database, 'sample')
        assert_stats(stats, expected, raw)

    if use_session:
        with client.session() as session:
            check(session)
    else:
        check(client)


@pytest.mark.asyncio
@pytest.mark.parametrize('use_session', [False, True], ids=['client', 'session'])
async def test_real_frontend_row_count_async(metadata_table, use_session):
    _, database, config = metadata_table
    client = AsyncClient()
    try:
        await client.connect(**config)

        async def check(executor):
            expected = (await executor.execute(f'SELECT COUNT(*) FROM {database}.sample')).scalar()
            raw = (await executor.metadata.scan(database, 'sample')).fetchall()
            stats = await executor.metadata.get_table_brief_stats(database, 'sample')
            assert_stats(stats, expected, raw)

        if use_session:
            async with client.session() as session:
                await check(session)
        else:
            await check(client)
    finally:
        await client.disconnect()
