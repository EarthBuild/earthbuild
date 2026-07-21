package main

import (
	"log"
	"net/http"

	"github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /two/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(hello.Greet("Friend")))
	})

	log.Fatal(http.ListenAndServe(":8080", mux))
}
