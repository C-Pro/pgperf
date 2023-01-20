drop schema if exists test cascade;
create schema test;

create table test.users (id bigint primary key, name varchar(128));

insert into test.users(id,name)
    select g, 'user ' || g::varchar
    from generate_series(1,1000000) g;


create table test.accounts (
    id bigserial primary key,
    user_id bigint references test.users(id),
    currency varchar(4),
    amount numeric
);

insert into test.accounts (user_id, currency, amount)
    select user_id,
           x.currency,
           random() * case
                            when x.currency = 'BTC' then 1
                            when x.currency = 'ETH' then 10
                            when x.currency = 'PTU' then 50000
                            when x.currency = 'IDRT' then 300000000
                      end as amount
    from generate_series(1,1000000) user_id
    cross join (select unnest as currency from unnest('{BTC,ETH,PTU,IDRT}'::varchar[])) x;


create table test.idr_rate (currency varchar(4), rate numeric);
insert into test.idr_rate(currency, rate) values
('IDRT', 1),
('BTC', 317000000),
('ETH', 23000000),
('PTU', 6000);
