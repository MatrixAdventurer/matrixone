-- CHAR/BINARY wider than 255 must fail with ER_TOO_BIG_FIELDLENGTH naming the column.
drop database if exists char_len_too_big;
create database char_len_too_big;
use char_len_too_big;
create table t1 (id int primary key, code char(256));
create table t2 (id int primary key, digest binary(300));
create table t3 (id int primary key, code char(255), digest binary(255));
show create table t3;
alter table t3 add column note char(256);
alter table t3 modify column code char(1000);
alter table t3 change column digest digest2 binary(256);
select cast('abc' as char(10));
drop database char_len_too_big;
