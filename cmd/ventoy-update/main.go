package main

import (
	"os"

	"ventoy-update/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
