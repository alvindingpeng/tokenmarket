package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/rhttp"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

func main() {
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Println("FATAL:", err)
		os.Exit(1)
	}
}

func run(dbPath, kekPath string) error {
	if err := secretstore.Init(secretstore.KEKConfig{File: kekPath}); err != nil {
		return err
	}
	conn, _ := gorm.Open(sqlite.Open("file:"+dbPath+"?mode=ro"), &gorm.Config{})
	var channel model.Channel
	conn.Where("id = ?", 41).First(&channel)
	var egresses []model.ChannelEgress
	conn.Where("channel_id = ?", 41).Find(&egresses)
	channel.Egresses = egresses
	var keys []model.ChannelKey
	conn.Where("channel_id = ?", 41).Find(&keys)
	token := model.ChannelKeyToken(keys[0])
	client, _ := rhttp.Direct()
	if eg, ok, _ := relay.PickEgress(channel, ""); ok && eg.ID != 0 {
		if c2, e2 := rhttp.Shared(strings.TrimSpace(eg.URL)); e2 == nil {
			client = c2
		}
	}
	_, dataBase := op.GeminiCLIBases(channel.ChannelConfig)
	fmt.Println("dataBase =", dataBase)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := op.RetrieveUserQuota(ctx, client, dataBase, token, channel.GeminiProject)
	if err != nil {
		return err
	}
	buckets := res.Buckets
	if buckets == nil && res.Quota != nil {
		buckets = res.Quota.Buckets
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].ModelID < buckets[j].ModelID })
	fmt.Printf("buckets=%d\n", len(buckets))
	for _, b := range buckets {
		flag := ""
		if b.RemainingFraction <= 0 {
			flag = "   <-- EXHAUSTED"
		}
		fmt.Printf("  %-42s remaining=%.4f reset=%s%s\n", b.ModelID, b.RemainingFraction, b.ResetTime, flag)
	}
	fmt.Println("AvailableModels:", res.AvailableModels())
	return nil
}
