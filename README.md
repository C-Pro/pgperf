# Some tips on postgresql performance

Database is often the main bottleneck in applications that need to serve large amounts of data per request, or experience high request rates.
This repository is a code that I use as a set of examples/demos for my talk on optimizing query performance in PostgreSQL database applications.

In this repository I use excellent [pgx](https://github.com/jackc/pgx) driver, that already includes many of optimizations that come in handy to write performant database layer code.

To run the code you need to start database and run benchmarks:

```
$ docker run --rm -v "$PWD:/docker-entrypoint-initdb.d" -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres
$ go test -benchmem -bench Benchmark .
```

Benchmarks demonstrate different methods of quering and inserting multiple records.



To demonstrate how query planning works we connect to postgres using `psql` shell.

```
$ psql -h localhost -U postgres postgres
```

At first we disable parallel sequential scanning (so the query plans will be easier to read).

```
set max_parallel_workers_per_gather = 0;
```

This statement changes value per session. So if you exit `psql` shell and log in again, you need to run it again.

Let's analyze the query, that returns top 10 users by total IDR value of their accounts.

```
explain analyze
select u.name,
       sum(a.amount * r.rate) as net
from test.users u
join test.accounts a on (a.user_id = u.id)
join test.idr_rate r on (a.currency = r.currency)
group by u.name
order by net desc
limit 10;
```

It runs about 5 seconds on my machine and uses sequential scans on all tables and hash-joins them together, then aggregates and gets the result:

```
                                                                       QUERY PLAN
--------------------------------------------------------------------------------------------------------------------------------------------------------
 Limit  (cost=648788.27..648788.30 rows=10 width=43) (actual time=4982.528..4982.533 rows=10 loops=1)
   ->  Sort  (cost=648788.27..651288.27 rows=1000000 width=43) (actual time=4861.120..4861.123 rows=10 loops=1)
         Sort Key: (sum((a.amount * r.rate))) DESC
         Sort Method: top-N heapsort  Memory: 26kB
         ->  HashAggregate  (cost=559991.13..627178.63 rows=1000000 width=43) (actual time=3713.510..4759.475 rows=1000000 loops=1)
               Group Key: u.name
               Planned Partitions: 64  Batches: 65  Memory Usage: 8209kB  Disk Usage: 203784kB
               ->  Hash Join  (cost=41037.09..231241.13 rows=4000000 width=28) (actual time=320.458..2549.413 rows=4000000 loops=1)
                     Hash Cond: ((a.currency)::text = (r.currency)::text)
                     ->  Hash Join  (cost=41036.00..176240.04 rows=4000000 width=27) (actual time=320.412..2007.263 rows=4000000 loops=1)
                           Hash Cond: (a.user_id = u.id)
                           ->  Seq Scan on accounts a  (cost=0.00..71968.00 rows=4000000 width=24) (actual time=0.028..417.802 rows=4000000 loops=1)
                           ->  Hash  (cost=22676.00..22676.00 rows=1000000 width=19) (actual time=319.781..319.782 rows=1000000 loops=1)
                                 Buckets: 131072  Batches: 8  Memory Usage: 7864kB
                                 ->  Seq Scan on users u  (cost=0.00..22676.00 rows=1000000 width=19) (actual time=0.006..185.632 rows=1000000 loops=1)
                     ->  Hash  (cost=1.04..1.04 rows=4 width=9) (actual time=0.024..0.024 rows=4 loops=1)
                           Buckets: 1024  Batches: 1  Memory Usage: 9kB
                           ->  Seq Scan on idr_rate r  (cost=0.00..1.04 rows=4 width=9) (actual time=0.019..0.020 rows=4 loops=1)
 Planning Time: 0.888 ms
 JIT:
   Functions: 27
   Options: Inlining true, Optimization true, Expressions true, Deforming true
   Timing: Generation 1.825 ms, Inlining 43.144 ms, Optimization 54.372 ms, Emission 50.521 ms, Total 149.861 ms
 Execution Time: 5112.470 ms
(24 rows)
```

Let's now add a new `deleted` field to the users table and soft-delete most of the users.

```
alter table test.users add deleted bool;
update test.users set deleted = true where id % 100 > 0;
```

This won't make our query faster, because it does not check deleted at field. Next we will add a partial index, containing both user name and id:

```
create unique index users_name_id_ui on test.users(name, id) where deleted is null;
```

Partial means that index will only contain non-deleted users keeping index small and fast. Since it contains name and id fields, postgres can not look at the table at all, using "index only" scan.

Now we can add this check to the query:

```
explain analyze
select u.name,
       sum(a.amount * r.rate) as net
from test.users u
join test.accounts a on (a.user_id = u.id)
join test.idr_rate r on (a.currency = r.currency)
where u.deleted is null
group by u.name
order by net desc
limit 10;
```

Now the query runs 10 times faster, because it analyzes much less users.

```
                                                                                  QUERY PLAN
------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
 Limit  (cost=84209.65..84209.67 rows=10 width=43) (actual time=508.952..508.956 rows=10 loops=1)
   ->  Sort  (cost=84209.65..84235.82 rows=10467 width=43) (actual time=508.949..508.951 rows=10 loops=1)
         Sort Key: (sum((a.amount * r.rate))) DESC
         Sort Method: top-N heapsort  Memory: 26kB
         ->  HashAggregate  (cost=83852.62..83983.46 rows=10467 width=43) (actual time=505.916..507.906 rows=10000 loops=1)
               Group Key: u.name
               Batches: 1  Memory Usage: 5009kB
               ->  Hash Join  (cost=494.88..83538.61 rows=41868 width=28) (actual time=15.277..489.070 rows=40000 loops=1)
                     Hash Cond: ((a.currency)::text = (r.currency)::text)
                     ->  Hash Join  (cost=493.79..82961.84 rows=41868 width=27) (actual time=15.099..482.973 rows=40000 loops=1)
                           Hash Cond: (a.user_id = u.id)
                           ->  Seq Scan on accounts a  (cost=0.00..71968.00 rows=4000000 width=24) (actual time=0.014..206.782 rows=4000000 loops=1)
                           ->  Hash  (cost=362.96..362.96 rows=10467 width=19) (actual time=13.938..13.939 rows=10000 loops=1)
                                 Buckets: 16384  Batches: 1  Memory Usage: 675kB
                                 ->  Index Only Scan using users_name_id_ui on users u  (cost=0.29..362.96 rows=10467 width=19) (actual time=0.208..8.541 rows=10000 loops=1)
                                       Heap Fetches: 0
                     ->  Hash  (cost=1.04..1.04 rows=4 width=9) (actual time=0.031..0.032 rows=4 loops=1)
                           Buckets: 1024  Batches: 1  Memory Usage: 9kB
                           ->  Seq Scan on idr_rate r  (cost=0.00..1.04 rows=4 width=9) (actual time=0.019..0.020 rows=4 loops=1)
 Planning Time: 1.038 ms
 Execution Time: 509.125 ms
 ```

 Let's see if we will get "nested loop" join between `users` and `accounts` tables if we limit number of accounts to three.

 ```
explain analyze
select u.name,
       sum(a.amount * r.rate) as net
from test.users u
join test.accounts a on (a.user_id = u.id)
join test.idr_rate r on (a.currency = r.currency)
where u.deleted is null
  and u.id in (100,200,300)
group by u.name
order by net desc
limit 10;
```

No, we still get hash join and a sequential scan on `accounts` table. We can fix this by adding extra index on accounts, that will help finding account by user id and planner will prefer nested loop join with only three index lookups, making our query much faster!

```
create index accounts_user_id_i on test.accounts(user_id);
```

Let's see if the plan has chandeg by running the same `explain analyze` query as the last time:

```
                                                                             QUERY PLAN
---------------------------------------------------------------------------------------------------------------------------------------------------------------------
 Limit  (cost=38.86..38.87 rows=1 width=43) (actual time=0.498..0.501 rows=3 loops=1)
   ->  Sort  (cost=38.86..38.87 rows=1 width=43) (actual time=0.496..0.498 rows=3 loops=1)
         Sort Key: (sum((a.amount * r.rate))) DESC
         Sort Method: quicksort  Memory: 25kB
         ->  GroupAggregate  (cost=38.80..38.85 rows=1 width=43) (actual time=0.469..0.477 rows=3 loops=1)
               Group Key: u.name
               ->  Sort  (cost=38.80..38.81 rows=4 width=28) (actual time=0.439..0.442 rows=12 loops=1)
                     Sort Key: u.name
                     Sort Method: quicksort  Memory: 25kB
                     ->  Nested Loop  (cost=0.85..38.76 rows=4 width=28) (actual time=0.109..0.413 rows=12 loops=1)
                           Join Filter: ((a.currency)::text = (r.currency)::text)
                           Rows Removed by Join Filter: 36
                           ->  Nested Loop  (cost=0.85..37.47 rows=4 width=27) (actual time=0.056..0.312 rows=12 loops=1)
                                 ->  Index Scan using users_pkey on users u  (cost=0.42..17.66 rows=1 width=19) (actual time=0.026..0.049 rows=3 loops=1)
                                       Index Cond: (id = ANY ('{100,200,300}'::bigint[]))
                                       Filter: (deleted IS NULL)
                                 ->  Index Scan using accounts_user_id_i on accounts a  (cost=0.43..19.77 rows=4 width=24) (actual time=0.042..0.084 rows=4 loops=3)
                                       Index Cond: (user_id = u.id)
                           ->  Materialize  (cost=0.00..1.06 rows=4 width=9) (actual time=0.004..0.005 rows=4 loops=12)
                                 ->  Seq Scan on idr_rate r  (cost=0.00..1.04 rows=4 width=9) (actual time=0.018..0.019 rows=4 loops=1)
 Planning Time: 1.796 ms
 Execution Time: 0.623 ms
(22 rows)
```

Yep, we have a nested loop and a much faster execution now. Yay!
