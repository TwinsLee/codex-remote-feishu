package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/jsonrpcutil"
)

const predictionSamplePrompt = "根据当前对话，为用户拟写下一条可发送的简短中文消息。只返回JSON对象，格式为{\"suggestion\":\"消息\"}。只给一条建议；没有合适建议时返回{\"suggestion\":\"\"}。不要执行任务，不要调用工具。"

type pendingPrediction struct {
	CommandID    string
	ThreadID     string
	SourceTurnID string
	Canceled     bool
}

func (t *Translator) translatePredictionRequest(command agentproto.Command) ([][]byte, error) {
	commandID := strings.TrimSpace(command.CommandID)
	threadID := strings.TrimSpace(command.Target.ThreadID)
	sourceTurnID := strings.TrimSpace(command.Prediction.SourceTurnID)
	if commandID == "" || threadID == "" || sourceTurnID == "" {
		return nil, fmt.Errorf("thread.prediction.request requires command, thread and source turn id")
	}
	for _, pending := range t.pendingPredictions {
		if pending.CommandID == commandID {
			return nil, fmt.Errorf("prediction command %s is already pending", commandID)
		}
	}
	requestID := t.NextRequest("prediction-sample")
	raw, err := json.Marshal(map[string]any{
		"id": requestID, "method": "thread/sample",
		"params": map[string]any{
			"threadId": threadID, "expectedTurnId": sourceTurnID,
			"prompt": predictionSamplePrompt, "threadSource": "composer_predictions",
			"turnTrigger": "composer_predictions", "disableMcp": true,
		},
	})
	if err != nil {
		return nil, err
	}
	t.pendingPredictions[requestID] = pendingPrediction{
		CommandID: commandID, ThreadID: threadID, SourceTurnID: sourceTurnID,
	}
	return [][]byte{append(raw, '\n')}, nil
}

func (t *Translator) translatePredictionCancel(command agentproto.Command) ([][]byte, error) {
	commandID := strings.TrimSpace(command.Prediction.RequestCommandID)
	if commandID == "" {
		return nil, fmt.Errorf("thread.prediction.cancel requires original request command id")
	}
	for requestID, pending := range t.pendingPredictions {
		if pending.CommandID != commandID || pending.Canceled {
			continue
		}
		cancelID := t.NextRequest("prediction-cancel")
		raw, err := json.Marshal(map[string]any{
			"id": cancelID, "method": "thread/sample/cancel",
			"params": map[string]any{"requestId": requestID},
		})
		if err != nil {
			return nil, err
		}
		pending.Canceled = true
		t.pendingPredictions[requestID] = pending
		t.pendingPredictionCancels[cancelID] = true
		return [][]byte{append(raw, '\n')}, nil
	}
	return nil, nil
}

func (t *Translator) observePredictionResponse(requestID string, message map[string]any) (Result, bool) {
	if method, _ := message["method"].(string); method != "" {
		return Result{}, false
	}
	if t.pendingPredictionCancels[requestID] {
		delete(t.pendingPredictionCancels, requestID)
		return Result{Suppress: true}, true
	}
	pending, owned := t.pendingPredictions[requestID]
	if !owned {
		return Result{}, false
	}
	delete(t.pendingPredictions, requestID)
	if pending.Canceled {
		return Result{Suppress: true}, true
	}
	event := agentproto.Event{
		Kind: agentproto.EventThreadPredictionResult, CommandID: pending.CommandID,
		ThreadID:   pending.ThreadID,
		Prediction: &agentproto.PredictionResult{SourceTurnID: pending.SourceTurnID},
	}
	if message["error"] != nil {
		code, _ := lookupMap(message, "error")["code"].(float64)
		event.ErrorMessage = jsonrpcutil.ExtractErrorMessage(message)
		event.Prediction.Unsupported = code == -32601 ||
			(code == -32600 && strings.HasPrefix(event.ErrorMessage, "Invalid request: unknown variant `thread/sample`,"))
		if event.ErrorMessage == "" {
			event.ErrorMessage = "prediction sample failed"
		}
	} else {
		result := lookupMap(message, "result")
		text, _ := result["text"].(string)
		if strings.TrimSpace(text) != "" {
			event.Prediction.SourceTurnID, _ = result["sourceTurnId"].(string)
			if event.Prediction.SourceTurnID != pending.SourceTurnID {
				event.ErrorMessage = "prediction source turn does not match the request"
			}
		}
		if event.ErrorMessage == "" {
			suggestion, err := parsePredictionSuggestion(text)
			if err != nil {
				event.ErrorMessage = err.Error()
			} else {
				event.Prediction.Suggestion = suggestion
			}
		}
	}
	return Result{Suppress: true, Events: []agentproto.Event{event}}, true
}

func parsePredictionSuggestion(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return "", fmt.Errorf("prediction JSON must be an object")
	}
	key, err := decoder.Token()
	if err != nil || key != "suggestion" {
		return "", fmt.Errorf("prediction JSON requires the suggestion key")
	}
	var value *string
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("invalid prediction JSON: %w", err)
	}
	if value == nil {
		return "", fmt.Errorf("prediction JSON requires a suggestion string")
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return "", fmt.Errorf("prediction JSON must contain only one suggestion")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("prediction JSON must contain one object")
	}
	suggestion := strings.TrimSpace(*value)
	if utf8.RuneCountInString(suggestion) > 200 {
		return "", fmt.Errorf("prediction suggestion exceeds 200 characters")
	}
	return suggestion, nil
}
