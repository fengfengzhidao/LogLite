//go:build logliteagent

package main

import (
	"log"
	"os"
)

func main() {
	if err := runAgent(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
