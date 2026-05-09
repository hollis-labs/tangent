package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestInterviewQuestion_ThreeQuestionsSameRoom(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	roomID, _ := createSession(t, rg, "interview-question")

	conn, _, err := websocket.Dial(context.Background(), rg.wsURL(roomID), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	cases := []struct {
		envelopeID        string
		threadID          string
		topicLabel        string
		selectedChoiceID  string
		answerText        string
		outputShapeSignal string
	}{
		{
			envelopeID:        "interview-env-1",
			threadID:          "goals",
			topicLabel:        "Workflow goals",
			selectedChoiceID:  "quality",
			answerText:        "Optimize for coherence before speed.",
			outputShapeSignal: "Bullets, then short rationale.",
		},
		{
			envelopeID:        "interview-env-2",
			threadID:          "constraints",
			topicLabel:        "Constraints",
			selectedChoiceID:  "time",
			answerText:        "Keep the protocol short enough for one sitting.",
			outputShapeSignal: "",
		},
		{
			envelopeID:        "interview-env-3",
			threadID:          "examples",
			topicLabel:        "Examples",
			selectedChoiceID:  "",
			answerText:        "Use one concrete sample per branch.",
			outputShapeSignal: "Numbered list with labels.",
		},
	}

	for _, tc := range cases {
		done := make(chan advanceResult, 1)
		go func(tc struct {
			envelopeID        string
			threadID          string
			topicLabel        string
			selectedChoiceID  string
			answerText        string
			outputShapeSignal string
		}) {
			done <- callInterviewQuestion(t, rg, roomID, tc.envelopeID, tc.threadID, tc.topicLabel)
		}(tc)

		frame := readWSFrame(t, conn, 3*time.Second)
		if frame["envelopeId"] != tc.envelopeID {
			t.Fatalf("envelopeId = %v, want %s", frame["envelopeId"], tc.envelopeID)
		}
		writeWSFrame(t, conn, map[string]any{
			"type":       "response",
			"envelopeId": tc.envelopeID,
			"response": map[string]any{
				"v":          1,
				"envelopeId": tc.envelopeID,
				"kind":       "data",
				"status":     "submitted",
				"payload": map[string]any{
					"answer_text":         tc.answerText,
					"selected_choice_id":  tc.selectedChoiceID,
					"thread_id":           tc.threadID,
					"topic_label":         tc.topicLabel,
					"output_shape_signal": tc.outputShapeSignal,
				},
			},
		})

		res := <-done
		if res.err != nil {
			t.Fatalf("interview question transport err: %v", res.err)
		}
		if res.result.IsError {
			t.Fatalf("interview question IsError=true: %s", extractText(t, res.result))
		}
	}

	getRes, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "tangent.session_get",
		Arguments: map[string]any{"roomID": roomID},
	})
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("session_get IsError=true: %s", extractText(t, getRes))
	}

	var state struct {
		EnvelopesHistory []struct {
			EnvelopeID string `json:"envelope_id"`
			Type       string `json:"type"`
			Interview  struct {
				ThreadID          string `json:"thread_id"`
				TopicLabel        string `json:"topic_label"`
				Prompt            string `json:"prompt"`
				AnswerText        string `json:"answer_text"`
				SelectedChoiceID  string `json:"selected_choice_id"`
				OutputShapeSignal string `json:"output_shape_signal"`
			} `json:"interview_question"`
		} `json:"envelopes_history"`
	}
	if err := json.Unmarshal([]byte(extractText(t, getRes)), &state); err != nil {
		t.Fatalf("unmarshal session_get: %v", err)
	}
	if len(state.EnvelopesHistory) != 3 {
		t.Fatalf("history len = %d, want 3", len(state.EnvelopesHistory))
	}
	for i, item := range state.EnvelopesHistory {
		want := cases[i]
		if item.Type != "tangent.interview-question" {
			t.Fatalf("history type = %q, want tangent.interview-question", item.Type)
		}
		if item.Interview.ThreadID != want.threadID || item.Interview.TopicLabel != want.topicLabel {
			t.Fatalf("interview metadata = %+v, want thread=%q topic=%q", item.Interview, want.threadID, want.topicLabel)
		}
		if item.Interview.AnswerText != want.answerText {
			t.Fatalf("answer_text = %q, want %q", item.Interview.AnswerText, want.answerText)
		}
		if item.Interview.SelectedChoiceID != want.selectedChoiceID {
			t.Fatalf("selected_choice_id = %q, want %q", item.Interview.SelectedChoiceID, want.selectedChoiceID)
		}
		if item.Interview.OutputShapeSignal != want.outputShapeSignal {
			t.Fatalf("output_shape_signal = %q, want %q", item.Interview.OutputShapeSignal, want.outputShapeSignal)
		}
		if item.Interview.Prompt == "" {
			t.Fatalf("prompt missing from interview history entry: %+v", item.Interview)
		}
	}
}

func TestInterviewQuestion_InputValidation(t *testing.T) {
	rg := newSessionRig(t)
	defer rg.cleanup()

	res, err := rg.mcpClient.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "tangent.interview_question",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   "interview-invalid-1",
				"type": "tangent.interview-question",
				"data": map[string]any{
					"prompt": "Missing thread/topic metadata should fail.",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("interview_question: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := extractText(t, res); got == "" {
		t.Fatal("expected validation error body")
	}
}

func callInterviewQuestion(
	t *testing.T,
	rg *sessionRig,
	roomID string,
	envelopeID string,
	threadID string,
	topicLabel string,
) advanceResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := rg.mcpClient.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "tangent.interview_question",
		Arguments: map[string]any{
			"envelope": map[string]any{
				"v":    1,
				"id":   envelopeID,
				"type": "tangent.interview-question",
				"data": map[string]any{
					"prompt":      "Tell me what matters most here.",
					"helper_text": "Be specific.",
					"thread_id":   threadID,
					"topic_label": topicLabel,
					"choices": []any{
						map[string]any{"id": "quality", "label": "Quality"},
						map[string]any{"id": "time", "label": "Time"},
					},
					"output_shape": map[string]any{
						"label": "Preferred output shape",
					},
				},
				"meta": map[string]any{
					"roomID": roomID,
				},
			},
		},
	})
	return advanceResult{result: res, err: err}
}
