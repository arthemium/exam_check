package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func utf16Length(value string) int {
	length := 0
	for _, codepoint := range value {
		length++
		if codepoint > 0xffff {
			length++
		}
	}
	return length
}

func canonicalParagraph(source transcriptParagraph, id string) checkedParagraph {
	paragraph := checkedParagraph{ID: id, PageNumber: source.PageNumber, IsTitle: source.IsTitle, Segments: source.Segments,
		UncertainRanges: make([]uncertainRange, 0)}
	var text strings.Builder
	for _, segment := range source.Segments {
		value := segment.Text
		if segment.Certainty == "unreadable" {
			value = "[unreadable]"
		} else if segment.Certainty == "uncertain" && value == "" {
			value = "[unclear]"
		}
		start := text.Len()
		text.WriteString(value)
		if segment.Certainty != "clear" {
			paragraph.UncertainRanges = append(paragraph.UncertainRanges, uncertainRange{start, text.Len(), segment.Certainty})
		}
	}
	paragraph.Text = text.String()
	return paragraph
}

func canonicalize(source transcription, pages []examPhoto) ([]checkedParagraph, error) {
	allowed, seen := map[int]bool{}, map[int]bool{}
	for _, page := range pages {
		allowed[page.Number] = true
	}
	paragraphs := make([]checkedParagraph, 0, len(source.Paragraphs))
	previous := 0
	for index, original := range source.Paragraphs {
		if !allowed[original.PageNumber] || original.PageNumber < previous {
			return nil, &CheckingError{"The model returned inconsistent page ordering.", true}
		}
		paragraph := canonicalParagraph(original, fmt.Sprintf("p%d", index+1))
		if strings.TrimSpace(paragraph.Text) == "" {
			return nil, &CheckingError{"The model returned an empty transcription.", true}
		}
		paragraphs = append(paragraphs, paragraph)
		previous, seen[original.PageNumber] = original.PageNumber, true
	}
	if len(seen) != len(allowed) {
		return nil, &CheckingError{"The model omitted one or more exam pages.", true}
	}
	return paragraphs, nil
}

func quoteBounds(text, quote string, occurrence int) (int, int, bool) {
	if quote == "" || occurrence < 1 {
		return 0, 0, false
	}
	start, cursor := 0, 0
	for range occurrence {
		found := strings.Index(text[cursor:], quote)
		if found < 0 {
			return 0, 0, false
		}
		start = cursor + found
		cursor = start + len(quote)
	}
	return start, cursor, true
}

func overlapsUncertainty(paragraph checkedParagraph, start, end int) bool {
	for _, uncertain := range paragraph.UncertainRanges {
		if start < uncertain.End && end > uncertain.Start {
			return true
		}
	}
	return false
}

func validateAnchor(item finding, paragraphs map[string]checkedParagraph) (*int, *int, error) {
	if item.Scope == "document" {
		if item.ParagraphID != nil || item.Quote != nil || item.Occurrence != nil {
			return nil, nil, errors.New("document finding has a text anchor")
		}
		return nil, nil, nil
	}
	if item.ParagraphID == nil {
		return nil, nil, errors.New("unknown paragraph")
	}
	paragraph, exists := paragraphs[*item.ParagraphID]
	if !exists {
		return nil, nil, errors.New("unknown paragraph")
	}
	if item.Scope == "paragraph" {
		if item.Quote != nil || item.Occurrence != nil || len(paragraph.UncertainRanges) > 0 {
			return nil, nil, errors.New("paragraph anchor is invalid or needs transcription review")
		}
		return nil, nil, nil
	}
	return validateSpan(item, paragraph)
}

func validateSpan(item finding, paragraph checkedParagraph) (*int, *int, error) {
	if item.Quote == nil || item.Occurrence == nil {
		return nil, nil, errors.New("missing quote or occurrence")
	}
	start, end, exists := quoteBounds(paragraph.Text, *item.Quote, *item.Occurrence)
	if !exists || overlapsUncertainty(paragraph, start, end) {
		return nil, nil, errors.New("quote is missing or overlaps unclear handwriting")
	}
	start, end = utf16Length(paragraph.Text[:start]), utf16Length(paragraph.Text[:end])
	return &start, &end, nil
}

func validateFinding(item finding, paragraphs map[string]checkedParagraph, checks []string) (checkedIssue, error) {
	requested := false
	for _, category := range checks {
		requested = requested || category == item.Category
	}
	if !requested {
		return checkedIssue{}, errors.New("category was not requested")
	}
	if item.Scope != "span" && isExactCorrection(item.Category) {
		return checkedIssue{}, errors.New("language correction needs an exact text anchor")
	}
	if strings.TrimSpace(item.Suggestion) == "" || strings.TrimSpace(item.Explanation) == "" {
		return checkedIssue{}, errors.New("missing suggestion or explanation")
	}
	start, end, err := validateAnchor(item, paragraphs)
	issue := checkedIssue{finding: item, Start: start, End: end, Kind: "correction"}
	if !isExactCorrection(item.Category) {
		issue.Kind = "suggestion"
	}
	return issue, err
}

func isExactCorrection(category string) bool {
	return category == "grammar" || category == "spelling" || category == "punctuation"
}

func validateFindings(source analysis, paragraphs []checkedParagraph, checks []string) ([]checkedIssue, []string) {
	lookup, seen := map[string]checkedParagraph{}, map[string]bool{}
	for _, paragraph := range paragraphs {
		lookup[paragraph.ID] = paragraph
	}
	issues, warnings := make([]checkedIssue, 0), make([]string, 0)
	for index, finding := range source.Findings {
		issue, err := validateFinding(finding, lookup, checks)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Finding %d omitted: %s.", index+1, err))
			continue
		}
		fingerprint, _ := json.Marshal(issue)
		if !seen[string(fingerprint)] {
			seen[string(fingerprint)] = true
			issue.ID = fmt.Sprintf("issue-%d", len(issues)+1)
			issues = append(issues, issue)
		}
	}
	return issues, warnings
}

func publicParagraph(paragraph checkedParagraph) checkedParagraph {
	ranges := make([]uncertainRange, 0, len(paragraph.UncertainRanges))
	for _, interval := range paragraph.UncertainRanges {
		interval.Start = utf16Length(paragraph.Text[:interval.Start])
		interval.End = utf16Length(paragraph.Text[:interval.End])
		ranges = append(ranges, interval)
	}
	paragraph.UncertainRanges = ranges
	return paragraph
}

func buildCheckResult(model string, paragraphs []checkedParagraph, source analysis, checks []string) (json.RawMessage, error) {
	issues, warnings := validateFindings(source, paragraphs, checks)
	result := checkResult{SchemaVersion: 1, Model: model, OffsetUnit: "utf16_code_units", Checks: checks,
		Paragraphs: make([]checkedParagraph, 0, len(paragraphs)), Issues: issues, ValidationWarnings: warnings}
	texts := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		result.Paragraphs = append(result.Paragraphs, publicParagraph(paragraph))
		result.NeedsTranscriptionReview = result.NeedsTranscriptionReview || len(paragraph.UncertainRanges) > 0
		texts = append(texts, paragraph.Text)
	}
	canonical, err := json.Marshal(result.Paragraphs)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	result.TranscriptVersion = hex.EncodeToString(digest[:])
	result.Transcript = strings.Join(texts, "\n\n")
	return json.Marshal(result)
}
