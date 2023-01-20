# Some tips on postgresql performance

Database is often the main bottleneck in applications that need to serve large amounts of data per request, or experience high request rates.
This repository is a code that I use as a set of examples/demos for my talk on optimizing query performance in PostgreSQL database applications.

In this repository I use excellent [pgx](https://github.com/jackc/pgx) driver, that already includes many of optimizations that come in handy to write performant database layer code.

To run the code you need to start database, install the schema and run benchmarks:

```
$ docker run --rm -v "$PWD:/docker-entrypoint-initdb.d" -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres
$ go test -bench Benchmark .
```
