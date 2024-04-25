https://docs.turso.tech/sdk/go/quickstart

Install libsql for Go
`go get github.com/tursodatabase/libsql-client-go/libsql`

Build and Run

```
docker build -t go-test
docker run -d -p 8080:8080 go-test

# test
curl http://localhost:8080/posts
```

Deploy

```
flyctl launch
```
