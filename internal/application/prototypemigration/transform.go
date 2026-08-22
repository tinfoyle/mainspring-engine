package prototypemigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidSnapshot = errors.New("prototype migration snapshot is invalid")
	ErrCorruptSource   = errors.New("prototype migration source is corrupt")
	ErrManifest        = errors.New("prototype migration manifest is invalid")
)

type Transformer struct{}

func NewTransformer() *Transformer { return &Transformer{} }

func (transformer *Transformer) Transform(accountID ids.AccountID, snapshot Snapshot) (Bundle, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(snapshot.TenantID) != nil || strings.TrimSpace(snapshot.Checkpoint) == "" {
		return Bundle{}, ErrInvalidSnapshot
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Bundle{}, err
	}
	manifest := Manifest{
		Version:   ManifestVersion,
		AccountID: accountID,
		Source: SourceCheckpoint{
			TenantID:       snapshot.TenantID,
			Checkpoint:     strings.TrimSpace(snapshot.Checkpoint),
			SchemaVersions: sortedStrings(snapshot.SchemaVersions),
			Inventory:      snapshot.Inventory,
		},
	}
	manifest.Source.InventorySHA256 = digestJSON(snapshot.Inventory)
	bundle := Bundle{Manifest: manifest, Objects: make(map[string][]byte)}

	documents, documentIndex, unresolved, err := transformDocuments(accountID, snapshot.Documents, bundle.Objects)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Manifest.Documents = documents
	bundle.Manifest.Unresolved = append(bundle.Manifest.Unresolved, unresolved...)

	facts, unresolved, err := transformFacts(accountID, snapshot.FactVersions, snapshot.CurrentFacts, documentIndex)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Manifest.Facts = facts
	bundle.Manifest.Unresolved = append(bundle.Manifest.Unresolved, unresolved...)

	baselines, unresolved := transformBaselines(snapshot.Assessments)
	bundle.Manifest.Baselines = baselines
	bundle.Manifest.Unresolved = append(bundle.Manifest.Unresolved, unresolved...)
	for _, deferred := range snapshot.Deferred {
		code := map[string]string{
			"source_item":        "connector_source_item_unbound",
			"interview_message":  "legacy_interview_message_report_only",
			"baseline_work_link": "legacy_work_link_requires_reconciliation",
			"research_result":    "public_capture_unbound",
		}[deferred.Kind]
		if code == "" {
			return Bundle{}, fmt.Errorf("%w: unknown deferred record kind %q", ErrCorruptSource, deferred.Kind)
		}
		bundle.Manifest.Unresolved = append(bundle.Manifest.Unresolved, issue(deferred.Kind, deferred.SourceID, code, deferred.Detail))
	}
	sortUnresolved(bundle.Manifest.Unresolved)
	bundle.Manifest.Totals = calculateTotals(bundle.Manifest)
	bundle.Manifest.ContentSHA256 = manifestDigest(bundle.Manifest)
	if err := Verify(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func validateSnapshot(snapshot Snapshot) error {
	var assessmentFacts, requirements, evidenceLinks uint64
	assessmentIDs := make(map[string]struct{}, len(snapshot.Assessments))
	for _, assessment := range snapshot.Assessments {
		if ids.Validate(assessment.ID) != nil || assessment.BaselineVersion == 0 || assessment.CreatedAt.IsZero() || assessment.UpdatedAt.Before(assessment.CreatedAt) {
			return fmt.Errorf("%w: invalid assessment %q", ErrCorruptSource, assessment.ID)
		}
		if _, exists := assessmentIDs[assessment.ID]; exists {
			return fmt.Errorf("%w: duplicate assessment %q", ErrCorruptSource, assessment.ID)
		}
		assessmentIDs[assessment.ID] = struct{}{}
		assessmentFacts += uint64(len(assessment.Facts))
		requirements += uint64(len(assessment.Requirements))
		for _, requirement := range assessment.Requirements {
			evidenceLinks += uint64(len(requirement.Evidence))
		}
	}
	if snapshot.Inventory.FactVersions != uint64(len(snapshot.FactVersions)) || snapshot.Inventory.CurrentFacts != uint64(len(snapshot.CurrentFacts)) || snapshot.Inventory.DocumentRevisions != uint64(len(snapshot.Documents)) || snapshot.Inventory.Assessments != uint64(len(snapshot.Assessments)) || snapshot.Inventory.AssessmentFacts != assessmentFacts || snapshot.Inventory.Requirements != requirements || snapshot.Inventory.EvidenceLinks != evidenceLinks {
		return fmt.Errorf("%w: source inventory does not match scanned records", ErrCorruptSource)
	}
	deferredCounts := make(map[string]uint64)
	deferredIDs := make(map[string]struct{}, len(snapshot.Deferred))
	for _, deferred := range snapshot.Deferred {
		key := deferred.Kind + ":" + deferred.SourceID
		if strings.TrimSpace(deferred.SourceID) == "" {
			return fmt.Errorf("%w: empty deferred record identity", ErrCorruptSource)
		}
		if _, exists := deferredIDs[key]; exists {
			return fmt.Errorf("%w: duplicate deferred record %q", ErrCorruptSource, key)
		}
		deferredIDs[key] = struct{}{}
		deferredCounts[deferred.Kind]++
	}
	if deferredCounts["source_item"] != snapshot.Inventory.SourceItems || deferredCounts["interview_message"] != snapshot.Inventory.InterviewMessages || deferredCounts["baseline_work_link"] != snapshot.Inventory.BaselineWorkLinks || deferredCounts["research_result"] != snapshot.Inventory.ResearchResults {
		return fmt.Errorf("%w: deferred inventory does not match scanned records", ErrCorruptSource)
	}
	factIDs := make(map[string]struct{}, len(snapshot.FactVersions))
	for _, fact := range snapshot.FactVersions {
		if _, exists := factIDs[fact.ID]; exists {
			return fmt.Errorf("%w: duplicate fact version %q", ErrCorruptSource, fact.ID)
		}
		factIDs[fact.ID] = struct{}{}
	}
	currentKeys := make(map[string]struct{}, len(snapshot.CurrentFacts))
	for _, fact := range snapshot.CurrentFacts {
		if _, exists := currentKeys[fact.Key]; exists {
			return fmt.Errorf("%w: duplicate current fact %q", ErrCorruptSource, fact.Key)
		}
		currentKeys[fact.Key] = struct{}{}
	}
	documentRevisions := make(map[string]struct{}, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		if _, exists := documentRevisions[document.RevisionID]; exists {
			return fmt.Errorf("%w: duplicate document revision %q", ErrCorruptSource, document.RevisionID)
		}
		documentRevisions[document.RevisionID] = struct{}{}
	}
	return nil
}

type documentTarget struct {
	RevisionID    string
	Revision      uint64
	ContentSHA256 string
}

func transformDocuments(accountID ids.AccountID, source []LegacyDocumentRevision, objects map[string][]byte) ([]DocumentPlan, map[string]documentTarget, []UnresolvedRecord, error) {
	items := append([]LegacyDocumentRevision(nil), source...)
	sort.Slice(items, func(left, right int) bool {
		if items[left].DocumentID != items[right].DocumentID {
			return items[left].DocumentID < items[right].DocumentID
		}
		if items[left].Revision != items[right].Revision {
			return items[left].Revision < items[right].Revision
		}
		return items[left].RevisionID < items[right].RevisionID
	})
	result := make([]DocumentPlan, 0, len(items))
	latest := make(map[string]documentTarget)
	unresolved := make([]UnresolvedRecord, 0)
	for _, item := range items {
		if ids.Validate(item.DocumentID) != nil || ids.Validate(item.RevisionID) != nil || item.Revision == 0 || item.CreatedAt.IsZero() || strings.TrimSpace(item.Name) == "" || len(item.StoredSHA256) != sha256.Size {
			return nil, nil, nil, fmt.Errorf("%w: invalid document revision identity %q", ErrCorruptSource, item.RevisionID)
		}
		digest := sha256.Sum256(item.Content)
		if !bytes.Equal(digest[:], item.StoredSHA256) {
			return nil, nil, nil, fmt.Errorf("%w: document revision %s checksum mismatch", ErrCorruptSource, item.RevisionID)
		}
		plan := DocumentPlan{
			SourceDocumentID:  item.DocumentID,
			SourceRevisionID:  item.RevisionID,
			SourceRevision:    item.Revision,
			Title:             boundedTitle(item.Name, item.Revision),
			Filename:          migrationFilename(item.Name, item.Revision),
			OriginalMediaType: strings.TrimSpace(item.MediaType),
			ImportMediaType:   "text/plain",
			ContentSHA256:     hex.EncodeToString(digest[:]),
			ByteSize:          int64(len(item.Content)),
			Action:            "import_reconstructed_text",
			CreatedAt:         item.CreatedAt.UTC(),
		}
		if item.DeletedAt != nil || item.Status == "deleted" {
			plan.Action = "report_only"
			unresolved = append(unresolved, issue("document", item.RevisionID, "legacy_document_deleted", "deleted prototype content is inventoried but must not be resurrected"))
		} else if len(item.Content) == 0 || int64(len(item.Content)) > knowledgedomain.MaximumExtractedTextBytes || !utf8.Valid(item.Content) || bytes.IndexByte(item.Content, 0) >= 0 {
			plan.Action = "report_only"
			unresolved = append(unresolved, issue("document", item.RevisionID, "legacy_document_text_invalid", "stored prototype text cannot enter the final document processor"))
		} else {
			chunks, chunkErr := knowledgeapp.ChunkExtractedText(item.Content)
			if chunkErr != nil {
				return nil, nil, nil, fmt.Errorf("%w: document revision %s chunking failed: %v", ErrCorruptSource, item.RevisionID, chunkErr)
			}
			plan.ExpectedChunks = uint32(len(chunks))
			plan.ObjectPath = path.Join("objects", plan.ContentSHA256+".txt")
			documentID, deriveErr := ids.Derive(string(accountID), "prototype-document:"+item.RevisionID)
			if deriveErr != nil {
				return nil, nil, nil, ErrInvalidSnapshot
			}
			revisionID, deriveErr := ids.Derive(documentID, "revision:1")
			if deriveErr != nil {
				return nil, nil, nil, ErrInvalidSnapshot
			}
			plan.TargetDocumentID, plan.TargetRevisionID = documentID, revisionID
			objects[plan.ObjectPath] = append([]byte(nil), item.Content...)
			current, exists := latest[item.DocumentID]
			if !exists || item.Revision > current.Revision {
				latest[item.DocumentID] = documentTarget{RevisionID: revisionID, Revision: item.Revision, ContentSHA256: plan.ContentSHA256}
			}
			if !prototypeTextNative(item.MediaType) {
				unresolved = append(unresolved, issue("document", item.RevisionID, "original_binary_unavailable", "prototype retained extracted text only; the final import is a text reconstruction with the original media type recorded as metadata"))
			}
		}
		result = append(result, plan)
	}
	return result, latest, unresolved, nil
}

func transformFacts(accountID ids.AccountID, versions []LegacyFactVersion, current []LegacyCurrentFact, documents map[string]documentTarget) ([]FactPlan, []UnresolvedRecord, error) {
	items := append([]LegacyFactVersion(nil), versions...)
	sort.Slice(items, func(left, right int) bool {
		if items[left].RecordedAt.Equal(items[right].RecordedAt) {
			return items[left].ID < items[right].ID
		}
		return items[left].RecordedAt.Before(items[right].RecordedAt)
	})
	currentByIndex := make(map[int]LegacyCurrentFact)
	matchedCurrent := make(map[string]bool)
	for _, value := range current {
		best := -1
		for index := range items {
			if matchedCurrent[value.Key] || !sameFact(items[index], value) {
				continue
			}
			if best < 0 || items[best].RecordedAt.Before(items[index].RecordedAt) {
				best = index
			}
		}
		if best >= 0 {
			currentByIndex[best] = value
			matchedCurrent[value.Key] = true
		}
	}
	for _, value := range current {
		if matchedCurrent[value.Key] {
			continue
		}
		encoded, _ := json.Marshal([]string{value.Key, value.Value, value.SourceType, value.SourceRef, value.UpdatedAt.UTC().Format(time.RFC3339Nano)})
		digest := sha256.Sum256(encoded)
		items = append(items, LegacyFactVersion{ID: "current-" + hex.EncodeToString(digest[:]), Key: value.Key, Label: value.Label, Value: value.Value, Scope: value.Scope, SourceType: value.SourceType, SourceRef: value.SourceRef, Confidence: value.Confidence, Sensitivity: value.Sensitivity, Status: value.Status, RecordedAt: value.UpdatedAt})
		currentByIndex[len(items)-1] = value
	}

	result := make([]FactPlan, 0, len(items))
	unresolved := make([]UnresolvedRecord, 0)
	for index, item := range items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Key) == "" || item.RecordedAt.IsZero() {
			return nil, nil, fmt.Errorf("%w: invalid fact version %q", ErrCorruptSource, item.ID)
		}
		confidence, err := confidencePermille(item.Confidence)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: fact %s confidence: %v", ErrCorruptSource, item.ID, err)
		}
		canonical, _ := json.Marshal(item.Value)
		contentDigest := sha256.Sum256(canonical)
		plan := FactPlan{SourceID: item.ID, Key: migrateFactKey(item.Key), Scope: "account", CanonicalValue: canonical, Confidence: confidence, Sensitivity: migrateSensitivity(item.Sensitivity), SourceType: item.SourceType, SourceReference: strings.TrimSpace(item.SourceRef), SourceRevision: item.RecordedAt.UTC().Format(time.RFC3339Nano), ContentSHA256: hex.EncodeToString(contentDigest[:]), Action: "report_only", RecordedAt: item.RecordedAt.UTC()}
		currentValue, isCurrent := currentByIndex[index]
		plan.SourceCurrent = isCurrent
		if strings.TrimSpace(item.Scope) != "" && item.Scope != "company" && item.Scope != "account" {
			unresolved = append(unresolved, issue("fact", item.ID, "legacy_scope_unbound", "only prototype company scope maps directly to final Account scope"))
			result = append(result, plan)
			continue
		}
		kind, reference, evidenceSHA256, importable, code, detail := resolveFactSource(item, currentValue, isCurrent, documents, plan.ContentSHA256)
		plan.EvidenceKind, plan.SourceReference, plan.EvidenceSHA256 = kind, reference, evidenceSHA256
		if importable {
			evidenceID, deriveErr := ids.Derive(string(accountID), "prototype-evidence:"+item.ID)
			if deriveErr != nil {
				return nil, nil, ErrInvalidSnapshot
			}
			claimID, deriveErr := ids.Derive(string(accountID), "prototype-claim:"+item.ID)
			if deriveErr != nil {
				return nil, nil, ErrInvalidSnapshot
			}
			plan.TargetEvidenceID, plan.TargetClaimID = evidenceID, claimID
			plan.Action = "import_proposed_claim"
			if kind == string(knowledgedomain.SourceAgentDerivation) {
				plan.Action = "import_non_authoritative_claim"
			}
		} else {
			unresolved = append(unresolved, issue("fact", item.ID, code, detail))
		}
		result = append(result, plan)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].RecordedAt.Equal(result[right].RecordedAt) {
			return result[left].SourceID < result[right].SourceID
		}
		return result[left].RecordedAt.Before(result[right].RecordedAt)
	})
	return result, unresolved, nil
}

func resolveFactSource(item LegacyFactVersion, current LegacyCurrentFact, isCurrent bool, documents map[string]documentTarget, valueSHA256 string) (kind, reference, evidenceSHA256 string, importable bool, code, detail string) {
	switch item.SourceType {
	case "owner":
		if isCurrent && current.ConfirmedAt != nil && !current.ConfirmedAt.IsZero() {
			return "", item.SourceRef, "", false, "owner_actor_missing", "prototype confirmation has no immutable confirming User identity; an Account member must review and re-propose it"
		}
		return "", item.SourceRef, "", false, "owner_confirmation_missing", "prototype fact history does not preserve a human confirmation event"
	case "document":
		target, exists := documents[strings.TrimSpace(item.SourceRef)]
		if exists {
			return string(knowledgedomain.SourceDocumentRevision), target.RevisionID, target.ContentSHA256, true, "", ""
		}
		return "", item.SourceRef, "", false, "document_source_unresolved", "legacy source_ref does not identify an importable document revision"
	case "agent":
		return string(knowledgedomain.SourceAgentDerivation), "prototype-agent-derivation:" + item.ID, valueSHA256, true, "", ""
	case "email", "google_drive":
		return "", item.SourceRef, "", false, "integration_grant_unbound", "legacy connector material has no immutable final source grant/capture binding"
	case "public_web":
		return "", item.SourceRef, "", false, "public_capture_unbound", "legacy public result lacks a frozen response revision and integrity-bound capture"
	default:
		return "", item.SourceRef, "", false, "source_type_unknown", "legacy source type is not recognized by the final Knowledge catalog"
	}
}

func transformBaselines(source []LegacyAssessment) ([]BaselinePlan, []UnresolvedRecord) {
	items := append([]LegacyAssessment(nil), source...)
	sort.Slice(items, func(left, right int) bool {
		if items[left].BaselineVersion != items[right].BaselineVersion {
			return items[left].BaselineVersion < items[right].BaselineVersion
		}
		return items[left].ID < items[right].ID
	})
	definitions := make(map[string]struct{})
	for _, definition := range baselinedomain.EvidenceCatalog() {
		definitions[definition.Code] = struct{}{}
	}
	result := make([]BaselinePlan, 0, len(items))
	unresolved := make([]UnresolvedRecord, 0)
	for _, item := range items {
		plan := BaselinePlan{SourceAssessmentID: item.ID, SourceVersion: item.BaselineVersion, SourceStatus: item.Status, SourcePhase: item.Phase, Action: "restart_from_reviewed_claims", NextReassessmentAt: utcPointer(item.NextReassessmentAt)}
		facts := append([]LegacyAssessmentFact(nil), item.Facts...)
		sort.Slice(facts, func(left, right int) bool { return facts[left].Key < facts[right].Key })
		for _, fact := range facts {
			question := migrateQuestionKey(fact.Key)
			canonical, _ := json.Marshal(fact.Value)
			action := "review_and_reanswer"
			if _, exists := baselinedomain.BaselineQuestion(question); !exists {
				action = "report_only"
				unresolved = append(unresolved, issue("baseline_answer", fact.ID, "question_key_unmapped", "prototype answer does not map to the governed Baseline interview"))
			}
			plan.Answers = append(plan.Answers, BaselineAnswerPlan{SourceFactID: fact.ID, QuestionKey: question, Value: canonical, Action: action})
		}
		requirements := append([]LegacyRequirement(nil), item.Requirements...)
		sort.Slice(requirements, func(left, right int) bool { return requirements[left].Key < requirements[right].Key })
		for _, requirement := range requirements {
			action := "review_against_current_catalog"
			if _, exists := definitions[requirement.Key]; !exists {
				action = "report_only"
				unresolved = append(unresolved, issue("baseline_requirement", requirement.ID, "requirement_code_unmapped", "prototype requirement is absent from the governed evidence catalog"))
			}
			plan.Requirements = append(plan.Requirements, RequirementReviewPlan{SourceRequirementID: requirement.ID, Code: requirement.Key, SourceStatus: requirement.Status, SourceDisposition: requirement.Disposition, Action: action, RenewalDueAt: utcPointer(requirement.RenewalDueAt), EvidenceLinkCount: uint32(len(requirement.Evidence))})
			for _, evidence := range requirement.Evidence {
				code, detail := "legacy_evidence_requires_revalidation", "legacy evidence links lack the final immutable evidence decision and citation-revision binding"
				if evidence.VerifiedAt == nil {
					code, detail = "evidence_not_human_verified", "legacy evidence link cannot satisfy a governed requirement without a human verification instant"
				}
				unresolved = append(unresolved, issue("baseline_evidence", evidence.ID, code, detail))
			}
		}
		result = append(result, plan)
	}
	return result, unresolved
}

func Verify(bundle Bundle) error {
	manifest := bundle.Manifest
	if manifest.Version != ManifestVersion || ids.Validate(string(manifest.AccountID)) != nil || ids.Validate(manifest.Source.TenantID) != nil || strings.TrimSpace(manifest.Source.Checkpoint) == "" || manifest.Source.InventorySHA256 != digestJSON(manifest.Source.Inventory) || manifest.ContentSHA256 != manifestDigest(manifest) || manifest.Totals != calculateTotals(manifest) {
		return ErrManifest
	}
	referenced := make(map[string]string)
	for _, document := range manifest.Documents {
		if document.Action != "import_reconstructed_text" {
			continue
		}
		body, exists := bundle.Objects[document.ObjectPath]
		digest := sha256.Sum256(body)
		if !exists || int64(len(body)) != document.ByteSize || hex.EncodeToString(digest[:]) != document.ContentSHA256 {
			return ErrManifest
		}
		referenced[document.ObjectPath] = document.ContentSHA256
	}
	if len(referenced) != len(bundle.Objects) {
		return ErrManifest
	}
	return nil
}

func manifestDigest(manifest Manifest) string {
	manifest.ContentSHA256 = ""
	return digestJSON(manifest)
}

func calculateTotals(manifest Manifest) ManifestTotals {
	result := ManifestTotals{FactRecords: uint64(len(manifest.Facts)), DocumentRevisions: uint64(len(manifest.Documents)), BaselineAssessments: uint64(len(manifest.Baselines)), UnresolvedRecords: uint64(len(manifest.Unresolved))}
	for _, fact := range manifest.Facts {
		if strings.HasPrefix(fact.Action, "import_") {
			result.ImportableClaims++
		}
	}
	for _, document := range manifest.Documents {
		if document.Action == "import_reconstructed_text" {
			result.ImportableDocuments++
			result.ExpectedChunks += uint64(document.ExpectedChunks)
		}
	}
	for _, baseline := range manifest.Baselines {
		result.BaselineAnswers += uint64(len(baseline.Answers))
		result.RequirementReviews += uint64(len(baseline.Requirements))
	}
	return result
}

func sameFact(version LegacyFactVersion, current LegacyCurrentFact) bool {
	return version.Key == current.Key && version.Value == current.Value && version.Scope == current.Scope && version.SourceType == current.SourceType && version.SourceRef == current.SourceRef && version.Confidence == current.Confidence && version.Sensitivity == current.Sensitivity && version.Status == current.Status
}

func confidencePermille(value string) (uint16, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("empty confidence")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 2 || (parts[0] != "0" && parts[0] != "1") {
		return 0, errors.New("confidence is outside zero to one")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 3 {
		return 0, errors.New("confidence has excessive precision")
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	if fraction != "" {
		if _, err := strconv.Atoi(fraction); err != nil {
			return 0, errors.New("confidence is malformed")
		}
	}
	if parts[0] == "1" {
		if fraction != "000" {
			return 0, errors.New("confidence is outside zero to one")
		}
		return 1000, nil
	}
	parsed, _ := strconv.Atoi(fraction)
	return uint16(parsed), nil
}

func migrateFactKey(value string) string {
	switch strings.TrimSpace(value) {
	case "organization.website":
		return "organization.website_url"
	case "organization.primary_jurisdiction":
		return "organization.primary_location"
	case "workforce.structure":
		return "organization.team_size"
	case "organization.immediate_concern":
		return "baseline.immediate_concern"
	default:
		return strings.TrimSpace(value)
	}
}

func migrateQuestionKey(value string) string {
	switch strings.TrimSpace(value) {
	case "business_name":
		return "organization.legal_name"
	case "website_url":
		return "organization.website_url"
	case "industry":
		return "organization.industry"
	case "primary_location":
		return "organization.primary_location"
	case "services":
		return "organization.services"
	case "team_size":
		return "organization.team_size"
	case "immediate_concern":
		return "baseline.immediate_concern"
	default:
		return strings.TrimSpace(value)
	}
}

func migrateSensitivity(value string) string {
	switch knowledgedomain.Sensitivity(strings.TrimSpace(value)) {
	case knowledgedomain.SensitivityPublic, knowledgedomain.SensitivityInternal, knowledgedomain.SensitivityConfidential, knowledgedomain.SensitivityRestricted:
		return strings.TrimSpace(value)
	default:
		return string(knowledgedomain.SensitivityInternal)
	}
}

func boundedTitle(name string, revision uint64) string {
	value := strings.TrimSpace(name)
	suffix := " [prototype revision " + strconv.FormatUint(revision, 10) + "]"
	if utf8.RuneCountInString(value)+utf8.RuneCountInString(suffix) > knowledgedomain.MaximumDocumentTitle {
		runes := []rune(value)
		runes = runes[:knowledgedomain.MaximumDocumentTitle-utf8.RuneCountInString(suffix)]
		value = strings.TrimSpace(string(runes))
	}
	return value + suffix
}

func migrationFilename(name string, revision uint64) string {
	base := strings.TrimSpace(name)
	if index := strings.LastIndex(base, "."); index > 0 {
		base = base[:index]
	}
	base = strings.Map(func(value rune) rune {
		if value == '/' || value == '\\' || value == 0 || value < 32 {
			return '_'
		}
		return value
	}, base)
	suffix := "-prototype-r" + strconv.FormatUint(revision, 10) + ".txt"
	runes := []rune(base)
	if len(runes)+len([]rune(suffix)) > knowledgedomain.MaximumDocumentFilename {
		runes = runes[:knowledgedomain.MaximumDocumentFilename-len([]rune(suffix))]
	}
	if strings.TrimSpace(string(runes)) == "" {
		return "prototype-document" + suffix
	}
	return strings.TrimSpace(string(runes)) + suffix
}

func prototypeTextNative(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	switch mediaType {
	case "text/plain", "text/markdown", "text/csv", "text/tab-separated-values", "text/html", "application/json", "application/xml", "application/yaml":
		return true
	default:
		return false
	}
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortUnresolved(values []UnresolvedRecord) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].Kind != values[right].Kind {
			return values[left].Kind < values[right].Kind
		}
		if values[left].SourceID != values[right].SourceID {
			return values[left].SourceID < values[right].SourceID
		}
		return values[left].Code < values[right].Code
	})
}

func digestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func issue(kind, sourceID, code, detail string) UnresolvedRecord {
	return UnresolvedRecord{Kind: kind, SourceID: sourceID, Code: code, Detail: detail}
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
