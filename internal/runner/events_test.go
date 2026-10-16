package runner

import "testing"

func TestParseResultEvent(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"hi","session_id":"sess_1","total_cost_usd":0.012,"is_error":false,"duration_ms":1234,"usage":{"input_tokens":10,"output_tokens":20}}`)
	e, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsResult() || e.SessionID != "sess_1" || e.TotalCostUSD != 0.012 {
		t.Fatalf("%+v", e)
	}
	if e.Usage.InputTokens != 10 || e.Usage.OutputTokens != 20 {
		t.Fatalf("%+v", e.Usage)
	}
}

func TestParseInitEvent(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","model":"claude-sonnet","session_id":"s","cwd":"/tmp","tools":["Bash"]}`)
	e, err := ParseEvent(line)
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsInit() {
		t.Fatal(e)
	}
}

func TestParseInvalid(t *testing.T) {
	_, err := ParseEvent([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected error")
	}
}
