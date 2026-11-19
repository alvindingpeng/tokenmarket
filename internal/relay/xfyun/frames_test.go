package xfyun

import "testing"

func TestFrameRoundTripAndCompletion(t *testing.T) {
	frame := &Frame{Header: FrameHeader{AppID: "app"}, Parameter: &ChatParameter{Chat: ChatConfig{Domain: "generalv3.5"}}, Payload: &FramePayload{Message: &ChatMessages{Text: []ChatMessage{{Role: RoleUser, Content: "hello"}}}}}
	data, err := frame.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFrame(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Parameter.Chat.Domain != "generalv3.5" || decoded.Payload.Message.Text[0].Content != "hello" {
		t.Fatalf("round trip mismatch")
	}
	response := &Frame{Header: FrameHeader{Code: 0}, Payload: &FramePayload{Choices: &ChatChoices{Status: StatusComplete, Text: []ChatChoice{{Content: "ok"}}}}}
	if !response.Done() || response.Content() != "ok" || response.Err() != nil {
		t.Fatalf("response helpers failed")
	}
}

func TestFrameError(t *testing.T) {
	err := (&Frame{Header: FrameHeader{Code: 10013, Message: "bad parameter", SID: "sid"}}).Err()
	if err == nil || err.Error() != "xunfei spark error 10013: bad parameter (sid sid)" {
		t.Fatalf("unexpected error: %v", err)
	}
}
