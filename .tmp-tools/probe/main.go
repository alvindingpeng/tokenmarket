package main

import (
	"fmt"
	"os"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
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
	if err := op.InitCache(); err != nil {
		panic(err)
	}
	ids := []int{654, 550, 477, 560, 644, 537, 478, 476}
	recent := op.ChannelModelRecentMetrics(ids, 7)
	fmt.Println("recent len:", len(recent))
	for _, id := range ids {
		m, ok := recent[id]
		if !ok {
			fmt.Println(id, "-> MISSING")
			continue
		}
		s, succ, wait := op.RecentMetricsOf(model.StatsMetrics(m))
		fmt.Println(id, "samples=", s, "success=", succ, "wait=", wait)
	}
}
