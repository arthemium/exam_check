package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testFinding() finding {
	paragraph, quote, occurrence := "p1", "go", 1
	return finding{Category: "grammar", Scope: "span", ParagraphID: &paragraph, Quote: &quote, Occurrence: &occurrence,
		Suggestion: "goes", Explanation: "Use the singular verb."}
}

func testParagraph(text, certainty string, number int) transcriptParagraph {
	return transcriptParagraph{PageNumber: number, Segments: []transcriptSegment{{Text: text, Certainty: certainty}}}
}

func TestCheckingRepeatedQuotesUseUTF16Offsets(t *testing.T) {
	paragraph := canonicalParagraph(testParagraph("😀 go go", "clear", 1), "p1")
	item := testFinding()
	*item.Occurrence = 2
	data, err := buildCheckResult("test-model", []checkedParagraph{paragraph}, analysis{[]finding{item}}, []string{"grammar"})
	if err != nil {
		t.Fatal(err)
	}
	var result checkResult
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 1 || *result.Issues[0].Start != 6 || *result.Issues[0].End != 8 {
		t.Fatalf("wrong quote offsets: %s", data)
	}
	if result.Transcript != "😀 go go" || result.OffsetUnit != "utf16_code_units" || len(result.TranscriptVersion) != 64 {
		t.Fatalf("wrong result contract: %s", data)
	}
}

func TestCheckingUncertaintyCannotBecomeLanguageCorrection(t *testing.T) {
	paragraph := canonicalParagraph(testParagraph("go", "uncertain", 1), "p1")
	issues, warnings := validateFindings(analysis{[]finding{testFinding()}}, []checkedParagraph{paragraph}, []string{"grammar"})
	if len(issues) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "unclear handwriting") {
		t.Fatalf("uncertain correction was accepted: %v %v", issues, warnings)
	}
	paragraph = canonicalParagraph(transcriptParagraph{PageNumber: 1, Segments: []transcriptSegment{
		{Text: "😀 ", Certainty: "clear"}, {Certainty: "unreadable"},
	}}, "p1")
	public := publicParagraph(paragraph)
	if public.Text != "😀 [unreadable]" || public.UncertainRanges[0].Start != 3 || public.UncertainRanges[0].End != 15 {
		t.Fatalf("wrong unreadable range: %+v", public)
	}
}

func TestCheckingUnsupportedFindingsAreDroppedAndDuplicatesRemoved(t *testing.T) {
	paragraphs := []checkedParagraph{canonicalParagraph(testParagraph("go", "clear", 1), "p1")}
	missing, unrequested, broad := testFinding(), testFinding(), testFinding()
	*missing.Quote = "went"
	unrequested.Category = "spelling"
	broad.Scope, broad.ParagraphID, broad.Quote, broad.Occurrence = "document", nil, nil, nil
	structure := broad
	structure.Category = "structure"
	findings := analysis{[]finding{missing, unrequested, broad, testFinding(), testFinding(), structure}}
	issues, warnings := validateFindings(findings, paragraphs, []string{"grammar", "structure"})
	if len(issues) != 2 || len(warnings) != 3 || issues[1].Kind != "suggestion" || issues[1].Start != nil {
		t.Fatalf("unexpected findings: %+v %v", issues, warnings)
	}
}

func TestCheckingRejectsIncompleteTranscripts(t *testing.T) {
	cases := []struct {
		paragraphs []transcriptParagraph
		pages      []examPhoto
	}{
		{[]transcriptParagraph{testParagraph("go", "clear", 2)}, []examPhoto{{Number: 1}}},
		{[]transcriptParagraph{testParagraph("go", "clear", 1)}, []examPhoto{{Number: 1}, {Number: 2}}},
		{[]transcriptParagraph{testParagraph("go", "clear", 2), testParagraph("go", "clear", 1)}, []examPhoto{{Number: 1}, {Number: 2}}},
		{[]transcriptParagraph{testParagraph(" ", "clear", 1)}, []examPhoto{{Number: 1}}},
	}
	for _, item := range cases {
		if _, err := canonicalize(transcription{Paragraphs: item.paragraphs}, item.pages); err == nil {
			t.Fatalf("accepted invalid transcript: %+v", item)
		}
	}
}

func TestCheckingStrictOutputSchemaRejectsMissingNullAndUnknownFields(t *testing.T) {
	for _, invalid := range []string{
		`{}`, `{"paragraphs":null}`, `{"paragraphs":[]}`, `{"paragraphs":[],"extra":"secret"}`,
		`{"paragraphs":[{"page_number":1,"segments":[{"certainty":"clear"}]}]}`,
		`{"paragraphs":[{"page_number":1,"segments":[{"text":"hello","certainty":"invented"}]}]}`,
		`{"paragraphs":[{"page_number":1,"segments":[{"text":null,"certainty":"clear"}]}]}`,
	} {
		var transcript transcription
		if err := decodeModelJSON([]byte(invalid), transcriptionSchema(), &transcript); err == nil {
			t.Fatalf("accepted invalid output %s", invalid)
		}
	}
	var feedback analysis
	if err := decodeModelJSON([]byte(`{"findings":[]}`), analysisSchema(), &feedback); err != nil {
		t.Fatal(err)
	}
}

func seedCheckingPhoto(t *testing.T, store *Store, config Config) []byte {
	t.Helper()
	seedWorkerExam(t, store, "exam")
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aWQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.UploadDir, "photo.png"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(`INSERT INTO exam_pages(id,exam_id,storage_key,mime_type,page_number,file_size) VALUES('page','exam','photo.png','image/png',1,?)`, len(data)); err != nil {
		t.Fatal(err)
	}
	return data
}

func writeProviderResponse(writer http.ResponseWriter, result any) {
	text, _ := json.Marshal(result)
	_ = json.NewEncoder(writer).Encode(map[string]any{"status": "completed", "output": []any{
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(text)}}},
	}})
}

func TestCheckingStreamsPhotosAndParsesBothOpenAIResponses(t *testing.T) {
	store, config := workerTestStore(t)
	photo := seedCheckingPhoto(t, store, config)
	var requests []map[string]json.RawMessage
	var requestLock sync.Mutex
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+config.OpenAIKey {
			t.Error("incorrect provider authentication or method")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requestLock.Lock()
		requests = append(requests, body)
		count := len(requests)
		requestLock.Unlock()
		if count == 1 {
			writeProviderResponse(writer, transcription{Paragraphs: []transcriptParagraph{testParagraph("She go home.", "clear", 1)}})
		} else {
			writeProviderResponse(writer, analysis{[]finding{testFinding()}})
		}
	}))
	defer provider.Close()
	checker := NewChecker(config, store).(*openAIChecker)
	checker.endpoint = provider.URL
	result, err := checker.Check(context.Background(), "exam")
	if err != nil {
		t.Fatal(err)
	}
	requestLock.Lock()
	defer requestLock.Unlock()
	if len(requests) != 2 {
		t.Fatalf("requests: %d", len(requests))
	}
	for _, request := range requests {
		if string(request["store"]) != "false" || !strings.Contains(string(request["text"]), `"strict":true`) {
			t.Fatalf("unsafe provider settings: %s", request["text"])
		}
	}
	if !strings.Contains(string(requests[0]["input"]), base64.StdEncoding.EncodeToString(photo)) {
		t.Fatal("photo bytes were not streamed correctly")
	}
	if !strings.Contains(string(requests[1]["input"]), `selected_checks`) || !strings.Contains(string(requests[1]["input"]), "grammar") {
		t.Fatal("selected checks were not submitted")
	}
	var decoded checkResult
	if err := json.Unmarshal(result, &decoded); err != nil || len(decoded.Issues) != 1 {
		t.Fatalf("result: %s %v", result, err)
	}
	if decoded.Transcript != "She go home." || *decoded.Issues[0].Start != 4 || *decoded.Issues[0].End != 6 {
		t.Fatalf("wrong transcript or anchors: %s", result)
	}
}

func TestCheckingProviderErrorsAreRedactedAndClassified(t *testing.T) {
	for _, status := range []int{400, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			store, config := workerTestStore(t)
			seedCheckingPhoto(t, store, config)
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				io.Copy(io.Discard, request.Body)
				writer.WriteHeader(status)
				io.WriteString(writer, "secret-provider-payload")
			}))
			defer provider.Close()
			checker := NewChecker(config, store).(*openAIChecker)
			checker.endpoint = provider.URL
			_, err := checker.Check(context.Background(), "exam")
			var failure *CheckingError
			if !errors.As(err, &failure) || strings.Contains(err.Error(), "secret") || failure.Retryable != (status != 400) {
				t.Fatalf("wrong provider error: %v", err)
			}
		})
	}
}

func TestCheckingRejectsIncompleteRefusedAndOversizedResponses(t *testing.T) {
	for _, body := range []string{
		`{"status":"incomplete","output":[]}`,
		`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"secret"}]}]}`,
		strings.Repeat("x", maxProviderResponse+1),
	} {
		store, config := workerTestStore(t)
		seedCheckingPhoto(t, store, config)
		provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			io.Copy(io.Discard, request.Body)
			io.WriteString(writer, body)
		}))
		checker := NewChecker(config, store).(*openAIChecker)
		checker.endpoint = provider.URL
		_, err := checker.Check(context.Background(), "exam")
		provider.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe provider response accepted: %v", err)
		}
	}
}

func TestCheckingDoesNotFollowRedirectsWithAPIKey(t *testing.T) {
	store, config := workerTestStore(t)
	seedCheckingPhoto(t, store, config)
	var contacted atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { contacted.Store(true) }))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		io.Copy(io.Discard, request.Body)
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer provider.Close()
	checker := NewChecker(config, store).(*openAIChecker)
	checker.endpoint = provider.URL
	if _, err := checker.Check(context.Background(), "exam"); err == nil || contacted.Load() {
		t.Fatal("redirect was followed")
	}
}

func TestCheckingPhotoPathsAndLimitsAreEnforced(t *testing.T) {
	store, config := workerTestStore(t)
	seedCheckingPhoto(t, store, config)
	checker := NewChecker(config, store).(*openAIChecker)
	for _, key := range []string{"../secret", "/etc/passwd", ".", ""} {
		if err := checker.preparePhoto(&examPhoto{Key: key, MIME: "image/jpeg"}); err == nil {
			t.Fatalf("accepted path %s", key)
		}
	}
	checker.config.MaxPageBytes = 1
	if _, _, err := checker.loadInput(context.Background(), "exam"); err == nil {
		t.Fatal("accepted oversized photo")
	}
}

func TestCheckingRequestHonorsCancellation(t *testing.T) {
	store, config := workerTestStore(t)
	seedCheckingPhoto(t, store, config)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	}))
	defer provider.Close()
	checker := NewChecker(config, store).(*openAIChecker)
	checker.endpoint = provider.URL
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := checker.Check(ctx, "exam"); err == nil {
		t.Fatal("canceled request unexpectedly completed")
	}
}
