package room

import (
	envelopes "github.com/hollis-labs/go-envelopes"
)

type InterviewQuestionChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type InterviewOutputShape struct {
	Label       string `json:"label,omitempty"`
	Help        string `json:"help,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

type InterviewQuestionHistory struct {
	ThreadID          string                    `json:"thread_id,omitempty"`
	TopicLabel        string                    `json:"topic_label,omitempty"`
	Prompt            string                    `json:"prompt,omitempty"`
	PromptMarkdown    string                    `json:"prompt_markdown,omitempty"`
	HelperText        string                    `json:"helper_text,omitempty"`
	Choices           []InterviewQuestionChoice `json:"choices,omitempty"`
	OutputShape       *InterviewOutputShape     `json:"output_shape,omitempty"`
	AnswerText        string                    `json:"answer_text,omitempty"`
	SelectedChoiceID  string                    `json:"selected_choice_id,omitempty"`
	OutputShapeSignal string                    `json:"output_shape_signal,omitempty"`
}

func buildInterviewQuestionHistory(
	env *envelopes.Envelope,
	resp *envelopes.Response,
) *InterviewQuestionHistory {
	if env == nil || env.Type != "tangent.interview-question" {
		return nil
	}

	data := env.Data
	if data == nil {
		return nil
	}

	history := &InterviewQuestionHistory{
		ThreadID:       readString(data, "thread_id"),
		TopicLabel:     readString(data, "topic_label"),
		Prompt:         readString(data, "prompt"),
		PromptMarkdown: readString(data, "prompt_markdown"),
		HelperText:     readString(data, "helper_text"),
		Choices:        readInterviewChoices(data["choices"]),
		OutputShape:    readInterviewOutputShape(data["output_shape"]),
	}

	if resp != nil {
		payload, _ := resp.Payload.(map[string]any)
		if payload != nil {
			history.AnswerText = readString(payload, "answer_text")
			history.SelectedChoiceID = readString(payload, "selected_choice_id")
			history.OutputShapeSignal = readString(payload, "output_shape_signal")
			if history.ThreadID == "" {
				history.ThreadID = readString(payload, "thread_id")
			}
			if history.TopicLabel == "" {
				history.TopicLabel = readString(payload, "topic_label")
			}
		}
	}

	if history.ThreadID == "" &&
		history.TopicLabel == "" &&
		history.Prompt == "" &&
		history.PromptMarkdown == "" &&
		history.HelperText == "" &&
		len(history.Choices) == 0 &&
		history.OutputShape == nil &&
		history.AnswerText == "" &&
		history.SelectedChoiceID == "" &&
		history.OutputShapeSignal == "" {
		return nil
	}

	return history
}

func readInterviewChoices(raw any) []InterviewQuestionChoice {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]InterviewQuestionChoice, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := readString(record, "id")
		if id == "" {
			continue
		}
		out = append(out, InterviewQuestionChoice{
			ID:          id,
			Label:       readString(record, "label"),
			Description: readString(record, "description"),
		})
	}
	return out
}

func readInterviewOutputShape(raw any) *InterviewOutputShape {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	shape := &InterviewOutputShape{
		Label:       readString(record, "label"),
		Help:        readString(record, "help"),
		Placeholder: readString(record, "placeholder"),
	}
	if shape.Label == "" && shape.Help == "" && shape.Placeholder == "" {
		return nil
	}
	return shape
}

func readString(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, _ := record[key].(string)
	return value
}
