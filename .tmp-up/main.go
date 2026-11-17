package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

// 直接打上游, 对比多个模型名与请求形态, 定位 404 的真实来源。
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
	if eg, ok, _ := relay.PickEgress(channel, ""); ok && eg.ID != 0 && eg.Kind != model.EgressKindDirect {
		if c2, e2 := rhttp.Shared(strings.TrimSpace(eg.URL)); e2 == nil {
			client = c2
		}
	}
	_, dataBase := op.GeminiCLIBases(channel.ChannelConfig)
	proj := channel.GeminiProject
	fmt.Println("dataBase =", dataBase, " project =", proj)

	for _, m := range []string{"gemini-3.5-flash-lite", "gemini-3.1-flash-lite", "gemini-2.5-flash-lite"} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		code, body := call(ctx, client, dataBase, token, proj, m, 200)
		cancel()
		fmt.Printf("  model=%-26s small  status=%d %s\n", m, code, trim(body, 110))
		time.Sleep(time.Second)
	}
	fmt.Println("--- 大上下文 (40 万字符) ---")
	for _, m := range []string{"gemini-3.5-flash-lite"} {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		code, body := call(ctx, client, dataBase, token, proj, m, 400000)
		cancel()
		fmt.Printf("  model=%-26s big    status=%d %s\n", m, code, trim(body, 130))
	}
	return nil
}

func trim(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}

func call(ctx context.Context, client *http.Client, base, token, project, modelName string, chars int) (int, string) {
	prompt := strings.Repeat("A", chars) + "\n\nReply with OK."
	inner := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
		"generationConfig": map[string]any{"maxOutputTokens": 16},
	}
	envelope := map[string]any{"project": project, "model": modelName, "request": inner, "requestType": "agent", "userAgent": "antigravity"}
	raw, _ := json.Marshal(envelope)
	url := strings.TrimRight(base, "/") + "/v1internal:streamGenerateContent?alt=sse"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity/1.200.0 windows/amd64")
	req.Header.Set("X-Goog-Api-Client", "google-cloud-sdk vscode_cloudshelleditor/0.1")
	req.Header.Set("Client-Metadata", `{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`)
	req.Header.Set("Accept", "text/event-stream")
	body, _ := json.Marshal(envelope)
	_ = body
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return resp.StatusCode, string(b)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var parsed map[string]any
		if json.Unmarshal([]byte(payload), &parsed) != nil {
			continue
		}
		if e, ok := parsed["error"].(map[string]any); ok {
			codeF, _ := e["code"].(float64)
			msg, _ := e["message"].(string)
			return int(codeF), msg
		}
	}
	return 200, "ok"
}
