package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/ferranbt/fastssz/sszgen"
)

func main() {
	var source string
	var objsStr string
	var output string
	var include string

	flag.StringVar(&source, "path", "", "")
	flag.StringVar(&objsStr, "objs", "", "")
	flag.StringVar(&output, "output", "", "")
	flag.StringVar(&include, "include", "", "")

	flag.Parse()

	targets := decodeList(objsStr)
	includeList := decodeList(include)

	if err := sszgen.Generate(source, includeList, targets, output); err != nil {
		fmt.Printf("[ERR]: %v", err)
	}
}

func decodeList(input string) []string {
	if input == "" {
		return []string{}
	}
	return strings.Split(strings.TrimSpace(input), ",")
}