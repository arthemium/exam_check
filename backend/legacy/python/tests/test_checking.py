import base64
import json
from types import SimpleNamespace

import httpx
import pytest
from openai import OpenAI

from app import checking
from app.checking import Analysis, CheckingError, Finding, Paragraph, Segment, Transcription


def paragraph(text: str, *, certainty="clear", page_number=1) -> Paragraph:
    return Paragraph(page_number=page_number, segments=[Segment(text=text, certainty=certainty)])


def finding(**overrides) -> Finding:
    values = dict(category="grammar", scope="span", paragraph_id="p1", quote="go", occurrence=1, suggestion="goes", explanation="Use the singular verb.")
    return Finding(**(values | overrides))


def response_payload(output: dict) -> dict:
    return {
        "id": "resp_test", "object": "response", "created_at": 1,
        "status": "completed", "model": "test-model", "parallel_tool_calls": False,
        "tool_choice": "auto", "tools": [],
        "output": [{
            "id": "msg_test", "type": "message", "role": "assistant", "status": "completed",
            "content": [{"type": "output_text", "text": json.dumps(output), "annotations": []}],
        }],
    }


def test_check_exam_uses_real_sdk_structured_output(monkeypatch, tmp_path):
    image_bytes = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aWQAAAABJRU5ErkJggg==")
    (tmp_path / "exam.png").write_bytes(image_bytes)
    settings = SimpleNamespace(openai_api_key="offline-test-key", openai_model="test-model", openai_timeout_seconds=1, upload_dir=tmp_path)
    monkeypatch.setattr(checking, "get_settings", lambda: settings)
    monkeypatch.setattr(checking, "load_exam_input", lambda _: (["grammar"], [checking.PageInput(1, "exam.png", "image/png")]))
    outputs = [
        {"paragraphs": [{"page_number": 1, "segments": [{"text": "She go home.", "certainty": "clear"}]}]},
        {"findings": [{"category": "grammar", "scope": "span", "paragraph_id": "p1", "quote": "go", "occurrence": 1, "suggestion": "goes", "explanation": "Use the singular verb."}]},
    ]
    requests = []

    def respond(request):
        assert request.method == "POST" and request.url.path == "/v1/responses"
        requests.append(json.loads(request.content))
        return httpx.Response(200, json=response_payload(outputs[len(requests) - 1]))

    transport = httpx.MockTransport(respond)
    monkeypatch.setattr(checking, "OpenAI", lambda **kwargs: OpenAI(http_client=httpx.Client(transport=transport), base_url="https://openai.example.test/v1", **kwargs))
    result = checking.check_exam("exam-test")

    assert len(requests) == 2
    assert all(body["store"] is False for body in requests)
    assert all(body["text"]["format"]["type"] == "json_schema" and body["text"]["format"]["strict"] for body in requests)
    image = requests[0]["input"][0]["content"][1]
    assert image["type"] == "input_image"
    assert base64.b64decode(image["image_url"].split(",", 1)[1]) == image_bytes
    submitted = json.loads(requests[1]["input"][0]["content"])
    assert submitted["selected_checks"] == ["grammar"]
    assert submitted["paragraphs"][0]["text"] == result["transcript"] == "She go home."
    issue = result["issues"][0]
    assert (issue["paragraph_id"], issue["start"], issue["end"], issue["suggestion"]) == ("p1", 4, 6, "goes")


def test_repeated_quotes_and_emoji_have_browser_offsets(monkeypatch):
    monkeypatch.setattr(checking, "get_settings", lambda: SimpleNamespace(openai_model="test-model"))
    paragraphs = checking.canonicalize(Transcription(paragraphs=[paragraph("😀 go go")]), {1})
    result = checking.build_result(paragraphs, Analysis(findings=[finding(occurrence=2)]), ["grammar"])
    issue = result["issues"][0]
    assert (issue["start"], issue["end"]) == (6, 8)
    assert result["transcript"] == "😀 go go"
    assert result["offset_unit"] == "utf16_code_units"
    assert len(result["transcript_version"]) == 64


def test_unclear_handwriting_is_not_marked_as_a_language_error(monkeypatch):
    monkeypatch.setattr(checking, "get_settings", lambda: SimpleNamespace(openai_model="test-model"))
    paragraphs = checking.canonicalize(Transcription(paragraphs=[paragraph("go", certainty="uncertain")]), {1})
    result = checking.build_result(paragraphs, Analysis(findings=[finding()]), ["grammar"])
    assert result["transcript"] == "go"
    assert result["needs_transcription_review"] is True
    assert result["issues"] == []
    assert "unclear handwriting" in result["validation_warnings"][0]


def test_missing_quotes_unrequested_checks_and_broad_grammar_are_omitted():
    paragraphs = checking.canonicalize(Transcription(paragraphs=[paragraph("go")]), {1})
    analysis = Analysis(findings=[
        finding(quote="went"),
        finding(category="spelling"),
        finding(scope="document", paragraph_id=None, quote=None, occurrence=None),
        finding(paragraph_id="invented"),
    ])
    issues, warnings = checking.validate_findings(analysis, paragraphs, ["grammar"])
    assert not issues
    assert len(warnings) == 4


def test_duplicate_findings_are_removed_and_structure_can_be_document_level():
    paragraphs = checking.canonicalize(Transcription(paragraphs=[paragraph("go")]), {1})
    structure = finding(category="structure", scope="document", paragraph_id=None, quote=None, occurrence=None, suggestion="Add a concluding paragraph.")
    issues, warnings = checking.validate_findings(Analysis(findings=[finding(), finding(), structure]), paragraphs, ["grammar", "structure"])
    assert len(issues) == 2
    assert not warnings
    assert issues[1]["kind"] == "suggestion"
    assert issues[1]["start"] is None


def test_unreadable_segments_keep_separate_utf16_ranges():
    source = Paragraph(page_number=1, segments=[Segment(text="😀 ", certainty="clear"), Segment(text="", certainty="unreadable")])
    canonical = checking.canonical_paragraph(source, "p1")
    result = checking.public_paragraph(canonical)
    assert result["text"] == "😀 [unreadable]"
    assert result["uncertain_ranges"] == [{"start": 3, "end": 15, "certainty": "unreadable"}]


@pytest.mark.parametrize("paragraphs,pages", [
    ([paragraph("go", page_number=2)], {1}),
    ([paragraph("go")], {1, 2}),
    ([paragraph("go", page_number=2), paragraph("go")], {1, 2}),
    ([paragraph(" ")], {1}),
])
def test_inconsistent_or_missing_pages_are_not_silently_accepted(paragraphs, pages):
    with pytest.raises(CheckingError):
        checking.canonicalize(Transcription(paragraphs=paragraphs), pages)
