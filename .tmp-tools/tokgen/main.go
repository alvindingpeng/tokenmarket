package main

import (
	"os"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

func main() {
	dbPath, kekFile := os.Args[1], os.Args[2]
	if err := secretstore.Init(secretstore.KEKConfig{File: kekFile}); err != nil {
		panic(err)
	}
	if err := db.InitDB("sqlite", dbPath, false); err != nil {
		panic(err)
	}
	if err := op.UserInit(); err != nil {
		panic(err)
	}
	for _, id := range []uint{1, 4} {
		if err := op.UserResetPassword(id, "p3check1122"); err != nil {
			panic(err)
		}
	}
	println("pw reset ok")
}
