package web

import (
	"reflect"
	"testing"
)

func TestParseClientMessage(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    clientMessage
	}{
		{
			name:    "JSON send with value",
			payload: `{"method":"send","value":"look","source":"input"}`,
			want:    clientMessage{Method: "send", Value: "look", Source: "input"},
		},
		{
			name:    "JSON send with empty value",
			payload: `{"method":"send","value":"","source":"input"}`,
			want:    clientMessage{Method: "send", Value: "", Source: "input"},
		},
		{
			name:    "Legacy plain text",
			payload: "look",
			want:    clientMessage{Method: "send", Value: "look", Source: "input"},
		},
		{
			name:    "Empty payload",
			payload: "  ",
			want:    clientMessage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseClientMessage([]byte(tt.payload)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseClientMessage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
