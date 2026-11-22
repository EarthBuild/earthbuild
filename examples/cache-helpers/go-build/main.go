// Package main is a program with enough imports to put something in the cache.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func main() {
	fmt.Println(strings.ToUpper("hello"), json.Valid(nil), http.StatusOK)
}
