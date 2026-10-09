package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/looplj/axonhub/llm/httpclient"
)

// maxMultipartMemory ReadForm 驻留内存的阈值; 超出部分落临时文件, 读后即弃。
// 客户端正文本就已整体读入内存, 这里只是重建表单时的二次缓冲。
const maxMultipartMemory = 32 << 20

// parseMultipartForm 解析已读入内存的 multipart 请求体, 供提取 model/stream 字段与重建表单使用。
// 返回的 *multipart.Form 持有全部字段与文件; 调用方在只读场景无需 RemoveAll(没有临时文件落盘的副本)。
func parseMultipartForm(raw *httpclient.Request) (*multipart.Form, error) {
	mediaType, params, err := mime.ParseMediaType(raw.Headers.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("invalid multipart content-type: %w", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return nil, errors.New("request is not multipart")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, errors.New("multipart boundary is missing")
	}
	reader := multipart.NewReader(bytes.NewReader(raw.Body), boundary)
	form, err := reader.ReadForm(maxMultipartMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to parse multipart form: %w", err)
	}
	return form, nil
}

// rewriteMultipartModel 把 multipart 表单中的 model 字段改写为当前上游模型名并重建表单。
// 每轮选路都会换目标, 因此每次都重建: 边界与 Content-Type 一并更新, 文件部分的文件名与
// Content-Type 原样保留, 多图 (image[]) 不受影响。
func rewriteMultipartModel(raw *httpclient.Request, modelName string) error {
	form, err := parseMultipartForm(raw)
	if err != nil {
		return err
	}
	defer form.RemoveAll()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// 字段部分: map 遍历无序, 但表单字段顺序无语义; model 替换为上游名, 其余原样写入。
	for key, values := range form.Value {
		for _, value := range values {
			if key == "model" {
				value = modelName
			}
			if err := writer.WriteField(key, value); err != nil {
				return fmt.Errorf("failed to write form field: %w", err)
			}
		}
	}
	if _, ok := form.Value["model"]; !ok {
		if err := writer.WriteField("model", modelName); err != nil {
			return fmt.Errorf("failed to write form field: %w", err)
		}
	}
	// 文件部分: 保留原文件名与 Content-Type, 二进制内容原样复制。
	for key, headers := range form.File {
		for _, header := range headers {
			part, err := writer.CreatePart(textproto.MIMEHeader{
				"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(key), escapeQuotes(header.Filename))},
				"Content-Type":        {header.Header.Get("Content-Type")},
			})
			if err != nil {
				return fmt.Errorf("failed to create form file: %w", err)
			}
			file, err := header.Open()
			if err != nil {
				return fmt.Errorf("failed to open form file: %w", err)
			}
			_, err = io.Copy(part, file)
			file.Close()
			if err != nil {
				return fmt.Errorf("failed to copy form file: %w", err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}
	raw.Body = body.Bytes()
	raw.Headers.Set("Content-Type", writer.FormDataContentType())
	return nil
}

// escapeQuotes 转义 Content-Disposition 中的引号, 与 mime/multipart 的内部转义规则一致。
func escapeQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`).Replace(value)
}

// multipartBodySummary 为日志生成去掉图片二进制的摘要正文: 字段与文件清单保留, 便于在日志页看懂请求。
func multipartBodySummary(form *multipart.Form) string {
	type fileSummary struct {
		Field    string `json:"field"`
		Filename string `json:"filename,omitempty"`
		Size     int64  `json:"size"`
	}
	summary := struct {
		Multipart bool          `json:"multipart"`
		Fields    map[string]string `json:"fields"`
		Files     []fileSummary `json:"files"`
	}{Multipart: true, Fields: map[string]string{}}
	for key, values := range form.Value {
		if len(values) > 0 {
			summary.Fields[key] = values[0]
		}
	}
	for key, headers := range form.File {
		for _, header := range headers {
			summary.Files = append(summary.Files, fileSummary{Field: key, Filename: header.Filename, Size: header.Size})
		}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return `{"multipart":true}`
	}
	return string(encoded)
}
