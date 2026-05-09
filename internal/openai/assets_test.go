package openai

import "testing"

func TestParseAssetResultScenarios(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantURL          string
		wantTaskID       string
		wantResponseID   string
		wantUpstreamCode string
	}{
		{
			name:           "markdown image url",
			input:          `{"content":"![image](https://cdn.example.com/a.png)","response_id":"resp_1"}`,
			wantURL:        "https://cdn.example.com/a.png",
			wantResponseID: "resp_1",
		},
		{
			name:    "download link text",
			input:   `[Download Video](https://cdn.example.com/a.mp4)`,
			wantURL: "https://cdn.example.com/a.mp4",
		},
		{
			name:           "nested payload url and task id",
			input:          `{"data":{"result":{"task_id":"task-123","url":"https://cdn.example.com/b.png"}},"response":{"created":{"response_id":"resp_nested"}}}`,
			wantURL:        "https://cdn.example.com/b.png",
			wantTaskID:     "task-123",
			wantResponseID: "resp_nested",
		},
		{
			name:       "video json response with wanx task id",
			input:      `{"success":true,"data":{"messages":[{"role":"assistant","chat_type":"t2v","extra":{"wanx":{"task_id":"task-video-1"}}}]}}`,
			wantTaskID: "task-video-1",
		},
		{
			name:    "video status success content url",
			input:   `{"chat_type":"t2v","task_status":"success","content":"https://cdn.example.com/video.mp4?key=abc"}`,
			wantURL: "https://cdn.example.com/video.mp4?key=abc",
		},
		{
			name:           "sse payload extraction",
			input:          "data: {\"choices\":[{\"delta\":{\"content\":\"https://cdn.example.com/c.png\"}}],\"response_id\":\"resp_sse\"}\n\n",
			wantURL:        "https://cdn.example.com/c.png",
			wantResponseID: "resp_sse",
		},
		{
			name:             "success false upstream error",
			input:            `{"success":false,"request_id":"req-1","data":{"code":"Bad_Request","details":"internal error"}}`,
			wantUpstreamCode: "Bad_Request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAssetResult([]byte(tt.input))
			if result.ContentURL != tt.wantURL {
				t.Fatalf("ContentURL = %q, want %q", result.ContentURL, tt.wantURL)
			}
			if tt.wantTaskID != "" && result.TaskID != tt.wantTaskID {
				t.Fatalf("TaskID = %q, want %q", result.TaskID, tt.wantTaskID)
			}
			if tt.wantResponseID != "" {
				if len(result.ResponseIDs) == 0 || result.ResponseIDs[0] != tt.wantResponseID {
					t.Fatalf("ResponseIDs = %#v, want first %q", result.ResponseIDs, tt.wantResponseID)
				}
			}
			if tt.wantUpstreamCode != "" {
				if result.UpstreamError == nil {
					t.Fatal("expected UpstreamError, got nil")
				}
				if result.UpstreamError.Code != tt.wantUpstreamCode {
					t.Fatalf("UpstreamError.Code = %q, want %q", result.UpstreamError.Code, tt.wantUpstreamCode)
				}
			}
		})
	}
}

func TestParseAssetResultKeepsSSEPayloads(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"生成中\"}}]}\n\n" +
		"data: {\"response_id\":\"resp_1\",\"task_id\":\"task_1\"}\n\n"

	result := parseAssetResult([]byte(input))
	if len(result.SSEPayloads) != 2 {
		t.Fatalf("SSEPayloads = %#v, want 2 payloads", result.SSEPayloads)
	}
	if result.SSEPayloads[0] != `{"choices":[{"delta":{"content":"生成中"}}]}` {
		t.Fatalf("first SSE payload = %q", result.SSEPayloads[0])
	}
	if result.TaskID != "task_1" {
		t.Fatalf("TaskID = %q, want task_1", result.TaskID)
	}
}

func TestAssetErrorPayloadIncludesRawSSEDebug(t *testing.T) {
	raw := []byte("data: {\"response_id\":\"resp_1\"}\n\n")
	result := parseAssetResult(raw)
	payload := assetErrorPayload(&assetParseError{message: "未能从上游响应中解析资源链接", result: result})

	debug, ok := payload["debug"].(map[string]any)
	if !ok {
		t.Fatalf("debug missing in payload: %#v", payload)
	}
	if debug["rawText"] != string(raw) {
		t.Fatalf("rawText = %q, want %q", debug["rawText"], string(raw))
	}
	payloads, ok := debug["ssePayloads"].([]string)
	if !ok || len(payloads) != 1 || payloads[0] != `{"response_id":"resp_1"}` {
		t.Fatalf("ssePayloads = %#v", debug["ssePayloads"])
	}
}

func TestExtractVideoTasksFromChatDetail(t *testing.T) {
	chatDetail := map[string]any{
		"data": map[string]any{
			"chat": map[string]any{
				"history": map[string]any{
					"messages": map[string]any{
						"m1": map[string]any{
							"id":          "resp_keep",
							"response_id": "resp_keep",
							"output":      map[string]any{"task_id": "task_keep"},
						},
						"m2": map[string]any{
							"id":          "resp_skip",
							"response_id": "resp_skip",
							"output":      map[string]any{"task_id": "task_skip"},
						},
					},
				},
			},
		},
	}

	tasks := extractVideoTasksFromChatDetail(chatDetail, []string{"resp_keep"})
	if len(tasks) != 1 || tasks[0] != "task_keep" {
		t.Fatalf("tasks = %#v, want [task_keep]", tasks)
	}
}
