drop database if exists fk_issue_33109;
create database fk_issue_33109;
use fk_issue_33109;

-- #33109: REPLACE on a parent whose ON DELETE CASCADE child is itself the
-- parent of an ON DELETE SET NULL grandchild. The equivalent DELETE already
-- worked; REPLACE used to fail planning with "Column remapping failed".
create table kp (id int primary key);
create table kc (id int primary key, p int, foreign key (p) references kp(id) on delete cascade);
create table kg (gid int primary key, pid int, foreign key (pid) references kc(id) on delete set null);
insert into kp values (1);
insert into kc values (10, 1);
insert into kg values (100, 10);
replace into kp values (1);
select id from kp;
select id, p from kc;
select gid, pid from kg;

-- DELETE control on a fresh chain: cascade the child and SET NULL the grandchild
insert into kp values (2);
insert into kc values (20, 2);
insert into kg values (200, 20);
delete from kp where id = 2;
select id from kp order by id;
select id, p from kc;
select gid, pid from kg order by gid;

-- one-level ON DELETE SET NULL REPLACE still nulls the child
create table sp (id int primary key);
create table sc (id int primary key, pid int, foreign key (pid) references sp(id) on delete set null);
insert into sp values (1);
insert into sc values (10, 1);
replace into sp values (1);
select id from sp;
select id, pid from sc;

create table op (id int primary key);
create table oc (id int, seq int, p int, primary key (id, seq), foreign key (p) references op(id) on delete cascade);
create table og (gid int primary key, pid int, foreign key (pid) references oc(id) on delete set null);
insert into op values (1), (2);
insert into oc values (10, 1, 1), (10, 2, 1), (20, 1, 2);
insert into og values (100, 10), (101, 10), (200, 20);
replace into op values (1);
select gid, pid from og order by gid;

create table ip (id int primary key);
create table ic (id int primary key, p int, foreign key (p) references ip(id) on delete cascade);
create table ig (gid int primary key, pid int, payload int, key ix (pid, payload), foreign key (pid) references ic(id) on delete set null);
insert into ip values (1), (2);
insert into ic values (10, 1), (20, 2);
insert into ig values (100, 10, 7), (200, 20, 7);
replace into ip values (1);
select gid, pid, payload from ig order by gid;

set @ix_table = (select index_table_name from mo_catalog.mo_indexes where name='ix' and table_id=(select rel_id from mo_catalog.mo_tables where reldatabase=database() and relname='ig') limit 1);
set @ix_query = concat('select __mo_index_pri_col as gid, serial_extract(__mo_index_idx_col, 0 as int) as pid, serial_extract(__mo_index_idx_col, 1 as int) as payload from `', @ix_table, '` order by gid');
prepare ix_rows from @ix_query;
execute ix_rows;

deallocate prepare ix_rows;
drop database fk_issue_33109;
