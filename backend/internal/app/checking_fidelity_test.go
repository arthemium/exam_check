package app

import (
	"encoding/json"
	"testing"
)

func TestCheckingPreservesTitleTyposAndWhitespace(t *testing.T) {
	title := testParagraph("  My Sumerr!  ", "clear", 1)
	title.IsTitle = true
	body := transcriptParagraph{PageNumber: 1, Segments: []transcriptSegment{
		{Text: "  She go ", Certainty: "clear"},
		{Text: " home.\n\tIts fun!  ", Certainty: "clear"},
	}}
	paragraphs, err := canonicalize(transcription{Paragraphs: []transcriptParagraph{title, body}}, []examPhoto{{Number: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !paragraphs[0].IsTitle || paragraphs[1].IsTitle || paragraphs[0].ID != "p1" || paragraphs[1].ID != "p2" {
		t.Fatalf("title metadata or reading order changed: %+v", paragraphs)
	}
	data, err := buildCheckResult("test", paragraphs, analysis{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result checkResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Transcript != "  My Sumerr!  \n\n  She go  home.\n\tIts fun!  " || !result.Paragraphs[0].IsTitle {
		t.Fatalf("student text was altered: %q", result.Transcript)
	}
	if count := answerWordCount(paragraphs); count != 5 {
		t.Fatalf("title affected answer word count: got %d, want 5", count)
	}
}

func TestTranscriptionSchemaRequiresTitleClassification(t *testing.T) {
	for _, flag := range []string{"true", "false"} {
		data := []byte(`{"paragraphs":[{"page_number":1,"is_title":` + flag + `,"segments":[{"text":"Written text","certainty":"clear"}]}]}`)
		var result transcription
		if err := decodeModelJSON(data, transcriptionSchema(), &result); err != nil {
			t.Fatalf("valid title classification rejected: %v", err)
		}
	}
	data := []byte(`{"paragraphs":[{"page_number":1,"segments":[{"text":"Written text","certainty":"clear"}]}]}`)
	if err := decodeModelJSON(data, transcriptionSchema(), &transcription{}); err == nil {
		t.Fatal("missing title classification accepted")
	}
}
