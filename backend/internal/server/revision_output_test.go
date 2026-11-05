package server

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestG2QuantityChangeEvidenceIDs(t *testing.T) {
	f := newAPI(t)
	body := `{"content":{"quantity_changes":[{"kind":"OTHER","direction":"UNKNOWN","quantity":null,"reason":"same"},{"kind":"OTHER","direction":"UNKNOWN","quantity":null,"reason":"same"}]}}`
	first := f.request("POST", "/api/v2/submissions", body, f.token, "change-evidence", 201)
	var out struct {
		Submission struct {
			ID string `json:"id"`
		} `json:"submission"`
		Revision struct {
			ID      string `json:"id"`
			Content struct {
				Changes []struct {
					ID string `json:"id"`
				} `json:"quantity_changes"`
			} `json:"content"`
		} `json:"current_revision"`
	}
	decode(t, first.Data, &out)
	changes := out.Revision.Content.Changes
	if len(changes) != 2 || changes[0].ID == "" || changes[0].ID == changes[1].ID {
		t.Fatal("missing evidence IDs", out)
	}
	replay := f.request("POST", "/api/v2/submissions", body, f.token, "change-evidence", 201)
	if !replay.Replay || first.Replay || string(replay.Data) != string(first.Data) {
		t.Fatal("replay lost evidence IDs")
	}
	get := f.call("GET", "/submissions/"+out.Submission.ID, "", 200)
	var current map[string]json.RawMessage
	decode(t, get.Data, &current)
	var original map[string]json.RawMessage
	decode(t, first.Data, &original)
	if string(current["current_revision"]) != string(original["current_revision"]) {
		t.Fatal("detail IDs changed")
	}
	f.call("PATCH", "/submissions/"+out.Submission.ID, fmt.Sprintf(`{"expected_version":1,"patch":{"quantity_changes":[{"id":%q,"kind":"OTHER","direction":"UNKNOWN","reason":"same"}]}}`, changes[0].ID), 400)
	f.call("PATCH", "/submissions/"+out.Submission.ID, `{"expected_version":1,"patch":{"remark":"next version"}}`, 200)
	var next struct {
		Revision struct {
			Content struct {
				Changes []struct {
					ID string `json:"id"`
				} `json:"quantity_changes"`
			} `json:"content"`
		} `json:"current_revision"`
	}
	decode(t, f.call("GET", "/submissions/"+out.Submission.ID, "", 200).Data, &next)
	if next.Revision.Content.Changes[0].ID == changes[0].ID {
		t.Fatal("IDs did not belong to saved revision")
	}
	f.call("GET", "/submissions/"+out.Submission.ID+"/revisions/"+out.Revision.ID, "", 200)
}
