package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

const WorkInputAction = "work.input"

const ownerInputEvidence = "The assigned agent exhausted the information available to this ticket before requesting owner input."

type WorkInputPayload struct {
	ParentWorkItemID string                 `json:"parent_work_item_id"`
	Questions        []string               `json:"questions"`
	Requirements     []WorkInputRequirement `json:"requirements,omitempty"`
}

type WorkInputRequirement struct {
	FactKey  string `json:"fact_key"`
	Question string `json:"question"`
	Reason   string `json:"reason,omitempty"`
}

type HumanInputAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type HumanInputRequest struct {
	ID                string
	ParentWorkItemID  string
	ParentNumber      int64
	ParentTitle       string
	WorkItemID        string
	WorkItemNumber    int64
	RunID             string
	PersonaName       string
	PersonaRole       string
	AssignedPersonaID string
	BoardroomID       string
	ConversationID    string
	Questions         []string
	Answers           []HumanInputAnswer
	DocumentIDs       []string
	Status            string
	RequestedAt       time.Time
	AnsweredAt        *time.Time
}

func DecodeWorkInputPayload(payload json.RawMessage) (WorkInputPayload, error) {
	var input WorkInputPayload
	if err := json.Unmarshal(payload, &input); err != nil {
		return WorkInputPayload{}, errors.New("work input payload is invalid")
	}
	input.ParentWorkItemID = strings.TrimSpace(input.ParentWorkItemID)
	if _, err := uuid.Parse(input.ParentWorkItemID); err != nil {
		return WorkInputPayload{}, errors.New("work input parent ticket is invalid")
	}
	rawQuestions := append([]string(nil), input.Questions...)
	if len(rawQuestions) == 0 {
		for _, requirement := range input.Requirements {
			rawQuestions = append(rawQuestions, requirement.Question)
		}
	}
	questions := make([]string, 0, len(rawQuestions))
	seen := map[string]bool{}
	for _, question := range rawQuestions {
		question = strings.Join(strings.Fields(strings.TrimSpace(question)), " ")
		key := strings.ToLower(question)
		if question == "" || len([]rune(question)) > 2000 || seen[key] {
			continue
		}
		seen[key] = true
		questions = append(questions, question)
	}
	if len(questions) == 0 || len(questions) > 8 {
		return WorkInputPayload{}, errors.New("work input must contain between 1 and 8 questions")
	}
	input.Questions = questions
	supplied := map[string]WorkInputRequirement{}
	for _, requirement := range input.Requirements {
		supplied[strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(requirement.Question)), " "))] = requirement
	}
	input.Requirements = input.Requirements[:0]
	for _, question := range input.Questions {
		requirement := supplied[strings.ToLower(question)]
		factKey := strings.ToLower(strings.TrimSpace(requirement.FactKey))
		if !validFactKey.MatchString(factKey) {
			factKey = FactKeyForQuestion(question)
		}
		input.Requirements = append(input.Requirements, WorkInputRequirement{
			FactKey: factKey, Question: question, Reason: strings.TrimSpace(requirement.Reason),
		})
	}
	return input, nil
}

var validFactKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,120}$`)

func (s *ApprovalService) ensureHumanInput(ctx context.Context, runID domain.RunID, invocationID domain.InvocationID, proposalPayload json.RawMessage) error {
	payload, err := DecodeWorkInputPayload(proposalPayload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var parentID, parentTitle, boardroomID, ownerID string
	var parentNumber int64
	err = tx.QueryRow(ctx, `
		SELECT work.id::text, work.number, work.title,
		       COALESCE(work.boardroom_id::text,''), owner_user.id::text
		FROM boardroom_runs run
		JOIN work_items work ON work.id=NULLIF(run.configuration_snapshot->>'work_item_id','')::uuid
		CROSS JOIN LATERAL (
			SELECT id FROM users WHERE role='owner' AND state='active' ORDER BY created_at LIMIT 1
		) owner_user
		WHERE run.id=$1
		FOR UPDATE OF work
	`, runID.String()).Scan(&parentID, &parentNumber, &parentTitle, &boardroomID, &ownerID)
	if err != nil {
		return fmt.Errorf("load human input parent ticket: %w", err)
	}
	if parentID != payload.ParentWorkItemID {
		return errors.New("work input parent does not match the run ticket")
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM human_input_requests
			WHERE invocation_id=$1 OR (parent_work_item_id=$2 AND status='pending')
		)
	`, invocationID.String(), parentID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit(ctx)
	}

	questionsJSON, _ := json.Marshal(payload.Questions)
	title := fmt.Sprintf("Provide information for #%04d: %s", parentNumber, parentTitle)
	if runes := []rune(title); len(runes) > 240 {
		title = string(runes[:240])
	}
	var checklist strings.Builder
	for index, question := range payload.Questions {
		fmt.Fprintf(&checklist, "%d. %s\n", index+1, question)
	}
	description := "The assigned agent completed everything possible with available documents and public research. It needs the following private business information before it can continue:\n\n" + strings.TrimSpace(checklist.String()) + "\n\nDefinition of done: Answer every item or state that the information is unavailable, and attach any requested records. The assigned agent will resume automatically."

	var childID string
	var childNumber int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO work_items (
			kind,title,description,priority,source,assigned_user_id,boardroom_id,parent_id,responsibility
		) VALUES ('ticket',$1,$2,'normal','persona',$3,NULLIF($4,'')::uuid,$5,'owner')
		RETURNING id::text,number
	`, title, description, ownerID, boardroomID, parentID).Scan(&childID, &childNumber); err != nil {
		return fmt.Errorf("create human input work item: %w", err)
	}
	var requestID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO human_input_requests (
			parent_work_item_id,work_item_id,run_id,invocation_id,questions
		) VALUES ($1,$2,$3,$4,$5)
		RETURNING id::text
	`, parentID, childID, runID.String(), invocationID.String(), questionsJSON).Scan(&requestID); err != nil {
		return fmt.Errorf("create human input request: %w", err)
	}
	if err := insertHumanInputRequirements(ctx, tx, requestID, payload.Requirements); err != nil {
		return fmt.Errorf("index human input facts: %w", err)
	}
	if err := reuseKnownHumanInputFacts(ctx, tx, requestID); err != nil {
		return fmt.Errorf("reuse known business facts: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE work_items SET status='waiting',updated_at=now() WHERE id=$1`, parentID); err != nil {
		return err
	}
	if err := finalizeSatisfiedRequests(ctx, tx, nil); err != nil {
		return fmt.Errorf("resolve known owner input: %w", err)
	}
	_ = childNumber
	return tx.Commit(ctx)
}

func (s *ApprovalService) ListHumanInputs(ctx context.Context, includeAnswered bool) ([]HumanInputRequest, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT request.id::text, request.parent_work_item_id::text, parent.number, parent.title,
		       request.work_item_id::text, child.number, COALESCE(request.run_id::text,''),
		       COALESCE(version.name,persona.name,''), COALESCE(version.role,persona.role,''),
		       COALESCE(parent.assigned_persona_id::text,version.persona_id::text,''), COALESCE(parent.boardroom_id::text,''),
		       COALESCE(parent.conversation_id::text,''), request.questions, request.response,
		       request.response_document_ids, request.status, request.requested_at, request.answered_at
		FROM human_input_requests request
		JOIN work_items parent ON parent.id=request.parent_work_item_id
		JOIN work_items child ON child.id=request.work_item_id
		LEFT JOIN agent_invocations invocation ON invocation.id=request.invocation_id
		LEFT JOIN persona_versions version ON version.id=invocation.persona_version_id
		LEFT JOIN personas persona ON persona.id=parent.assigned_persona_id
		WHERE ($1 OR request.status='pending')
		ORDER BY CASE request.status WHEN 'pending' THEN 0 ELSE 1 END, request.requested_at DESC
	`, includeAnswered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HumanInputRequest
	for rows.Next() {
		var item HumanInputRequest
		var questionsJSON, answersJSON, documentsJSON []byte
		if err := rows.Scan(
			&item.ID, &item.ParentWorkItemID, &item.ParentNumber, &item.ParentTitle,
			&item.WorkItemID, &item.WorkItemNumber, &item.RunID, &item.PersonaName, &item.PersonaRole,
			&item.AssignedPersonaID, &item.BoardroomID, &item.ConversationID, &questionsJSON, &answersJSON,
			&documentsJSON, &item.Status, &item.RequestedAt, &item.AnsweredAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(questionsJSON, &item.Questions); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(answersJSON, &item.Answers)
		_ = json.Unmarshal(documentsJSON, &item.DocumentIDs)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *ApprovalService) GetHumanInput(ctx context.Context, requestID string) (HumanInputRequest, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return HumanInputRequest{}, pgx.ErrNoRows
	}
	items, err := s.ListHumanInputs(ctx, true)
	if err != nil {
		return HumanInputRequest{}, err
	}
	for _, item := range items {
		if item.ID == requestID {
			return item, nil
		}
	}
	return HumanInputRequest{}, pgx.ErrNoRows
}

func (s *ApprovalService) AnswerHumanInput(ctx context.Context, requestID, userID string, answers []HumanInputAnswer, documentIDs []string) (HumanInputRequest, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return HumanInputRequest{}, pgx.ErrNoRows
	}
	if _, err := uuid.Parse(userID); err != nil {
		return HumanInputRequest{}, errors.New("answering user is invalid")
	}
	request, err := s.GetHumanInput(ctx, requestID)
	if err != nil {
		return HumanInputRequest{}, err
	}
	if request.Status != "pending" {
		return HumanInputRequest{}, errors.New("this information request has already been answered")
	}
	if len(answers) != len(request.Questions) {
		return HumanInputRequest{}, errors.New("answer every requested item")
	}
	for index := range answers {
		answers[index].Question = request.Questions[index]
		answers[index].Answer = strings.TrimSpace(answers[index].Answer)
		if answers[index].Answer == "" || len([]rune(answers[index].Answer)) > 12000 {
			return HumanInputRequest{}, fmt.Errorf("answer %d must contain between 1 and 12,000 characters", index+1)
		}
	}
	answersJSON, _ := json.Marshal(answers)
	if documentIDs == nil {
		documentIDs = []string{}
	}
	documentsJSON, _ := json.Marshal(documentIDs)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return HumanInputRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		WITH answered AS (
			UPDATE human_input_requests
			SET status='answered',response=$2,response_document_ids=$3,answered_at=now(),answered_by=$4
			WHERE id=$1 AND status='pending'
			RETURNING work_item_id,parent_work_item_id
		)
		UPDATE work_items work SET status='done',completed_at=now(),updated_at=now()
		FROM answered WHERE work.id=answered.work_item_id
	`, requestID, answersJSON, documentsJSON, userID)
	if err != nil {
		return HumanInputRequest{}, err
	}
	if tag.RowsAffected() == 0 {
		return HumanInputRequest{}, errors.New("this information request is no longer pending")
	}
	if err := insertHumanInputQuestionLinks(ctx, tx, request.ID, request.Questions); err != nil {
		return HumanInputRequest{}, err
	}
	for index, item := range answers {
		key := FactKeyForQuestion(item.Question)
		if err := tx.QueryRow(ctx, `
			SELECT fact_key FROM human_input_question_facts
			WHERE request_id=$1 AND question_index=$2
		`, request.ID, index).Scan(&key); err != nil {
			return HumanInputRequest{}, err
		}
		label, _ := factPresentation(key, item.Question)
		if _, err := tx.Exec(ctx, `
			UPDATE human_input_question_facts SET fact_key=$3,status='answered',answer=$4,answered_at=now(),updated_at=now()
			WHERE request_id=$1 AND question_index=$2
		`, request.ID, index, key, item.Answer); err != nil {
			return HumanInputRequest{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO business_knowledge_facts(fact_key,label,value,source_type,source_ref,confidence,confirmed_at)
			VALUES ($1,$2,$3,'owner',$4,1,now())
			ON CONFLICT (fact_key) DO UPDATE SET label=EXCLUDED.label,value=EXCLUDED.value,source_type='owner',
			    source_ref=EXCLUDED.source_ref,confidence=1,status='active',confirmed_at=now(),updated_at=now()
		`, key, label, item.Answer, "human_input:"+request.ID); err != nil {
			return HumanInputRequest{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return HumanInputRequest{}, err
	}
	request.Answers = answers
	request.DocumentIDs = documentIDs
	request.Status = "answered"
	now := time.Now()
	request.AnsweredAt = &now
	return request, nil
}

type legacyOwnerInputAction struct {
	ApprovalID   string
	ActionID     string
	RunID        string
	InvocationID string
	Payload      TicketCreatePayload
	Reason       string
}

var numberedQuestion = regexp.MustCompile(`(?m)^\s*\d+\.\s+(.+?)\s*$`)

func legacyProposalQuestions(payload TicketCreatePayload, reason string) []string {
	if marker := strings.Index(payload.Description, "Please answer or provide:"); marker >= 0 {
		section := payload.Description[marker+len("Please answer or provide:"):]
		if end := strings.Index(strings.ToLower(section), "definition of done:"); end >= 0 {
			section = section[:end]
		}
		matches := numberedQuestion.FindAllStringSubmatch(section, -1)
		questions := make([]string, 0, len(matches))
		for _, match := range matches {
			questions = append(questions, strings.TrimSpace(match[1]))
		}
		if len(questions) > 0 {
			return questions
		}
	}
	if marker := strings.Index(payload.Description, "Question:"); marker >= 0 {
		section := payload.Description[marker+len("Question:"):]
		if end := strings.Index(strings.ToLower(section), "definition of done:"); end >= 0 {
			section = section[:end]
		}
		if question := strings.TrimSpace(section); question != "" {
			return []string{question}
		}
	}
	if reason = strings.TrimSpace(reason); reason != "" && !strings.HasPrefix(reason, "The assigned agent needs ") {
		return []string{reason}
	}
	return nil
}

// MigratePendingOwnerInputs moves question-like legacy ticket approvals onto
// the reversible human-input workflow. It preserves their audit records as
// canceled actions and returns the runs that should be awakened so Temporal can
// observe that no approval remains pending.
func (s *ApprovalService) MigratePendingOwnerInputs(ctx context.Context) ([]domain.RunID, error) {
	evidenceJSON, _ := json.Marshal([]string{ownerInputEvidence})
	rows, err := s.pool.Query(ctx, `
		SELECT approval.id::text, action.id::text, action.run_id::text,
		       action.invocation_id::text, action.request_payload, action.reason
		FROM approval_requests approval
		JOIN external_actions action ON action.id=approval.action_id
		WHERE approval.status='pending' AND action.action_type=$1
		  AND action.evidence @> $2::jsonb
		ORDER BY action.run_id, approval.requested_at
	`, TicketCreateAction, string(evidenceJSON))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string][]legacyOwnerInputAction{}
	for rows.Next() {
		var item legacyOwnerInputAction
		var raw []byte
		if err := rows.Scan(&item.ApprovalID, &item.ActionID, &item.RunID, &item.InvocationID, &raw, &item.Reason); err != nil {
			return nil, err
		}
		payload, err := DecodeTicketCreatePayload(raw)
		if err != nil || payload.ParentWorkItemID == "" {
			continue
		}
		item.Payload = payload
		groups[item.RunID+":"+payload.ParentWorkItemID] = append(groups[item.RunID+":"+payload.ParentWorkItemID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var resumed []domain.RunID
	for _, group := range groups {
		questions := []string{}
		seen := map[string]bool{}
		for _, item := range group {
			for _, question := range legacyProposalQuestions(item.Payload, item.Reason) {
				key := strings.ToLower(strings.TrimSpace(question))
				if key != "" && !seen[key] && len(questions) < 8 {
					seen[key] = true
					questions = append(questions, strings.TrimSpace(question))
				}
			}
		}
		if len(questions) == 0 {
			continue
		}
		runID, err := domain.ParseRunID(group[0].RunID)
		if err != nil {
			return nil, err
		}
		invocationID, err := domain.ParseInvocationID(group[0].InvocationID)
		if err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(WorkInputPayload{ParentWorkItemID: group[0].Payload.ParentWorkItemID, Questions: questions})
		if err := s.ensureHumanInput(ctx, runID, invocationID, payload); err != nil {
			return nil, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range group {
			if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status='canceled',decided_at=now() WHERE id=$1 AND status='pending'`, item.ApprovalID); err != nil {
				_ = tx.Rollback(ctx)
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE external_actions SET status='rejected',last_error='Moved to Your Turn input request',updated_at=now() WHERE id=$1`, item.ActionID); err != nil {
				_ = tx.Rollback(ctx)
				return nil, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		resumed = append(resumed, runID)
	}
	return resumed, nil
}

// RecoverMissingHumanInputs replays the durable action projection for failed
// ticket runs whose agent invocation succeeded but whose owner-input request
// was not committed. The projection is idempotent by invocation ID.
func (s *ApprovalService) RecoverMissingHumanInputs(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT invocation.id::text
		FROM agent_invocations invocation
		JOIN boardroom_runs run ON run.id=invocation.run_id AND run.status='failed'
		WHERE invocation.status='succeeded'
		  AND EXISTS (
			SELECT 1
			FROM jsonb_array_elements(COALESCE(invocation.result_payload->'structured'->'proposed_actions','[]'::jsonb)) proposal
			WHERE proposal->>'action_type'=$1
		  )
		  AND NOT EXISTS (SELECT 1 FROM human_input_requests request WHERE request.invocation_id=invocation.id)
		ORDER BY invocation.id::text
	`, WorkInputAction)
	if err != nil {
		return 0, err
	}
	var invocationIDs []domain.InvocationID
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return 0, err
		}
		invocationID, err := domain.ParseInvocationID(value)
		if err != nil {
			rows.Close()
			return 0, err
		}
		invocationIDs = append(invocationIDs, invocationID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, invocationID := range invocationIDs {
		if err := s.EnsureInvocationActions(ctx, invocationID); err != nil {
			return 0, fmt.Errorf("recover owner input for invocation %s: %w", invocationID.String(), err)
		}
	}
	return len(invocationIDs), nil
}

// RunsAwaitingNoApproval finds workflows stranded after their last pending
// approval was migrated, approved, rejected, or canceled without a signal.
func (s *ApprovalService) RunsAwaitingNoApproval(ctx context.Context) ([]domain.RunID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT run.id::text
		FROM boardroom_runs run
		WHERE run.status='awaiting_approval'
		  AND NOT EXISTS (
			SELECT 1 FROM approval_requests approval
			WHERE approval.run_id=run.id AND approval.status='pending'
		  )
		ORDER BY run.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runIDs []domain.RunID
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		runID, err := domain.ParseRunID(value)
		if err != nil {
			return nil, err
		}
		runIDs = append(runIDs, runID)
	}
	return runIDs, rows.Err()
}

func (s *ApprovalService) YourTurnCounts(ctx context.Context) (inputs, reviews, approvals int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM human_input_requests WHERE status='pending'),
		  (SELECT count(*) FROM approval_requests request JOIN external_actions action ON action.id=request.action_id WHERE request.status='pending' AND action.action_type=$1),
		  (SELECT count(*) FROM approval_requests request JOIN external_actions action ON action.id=request.action_id WHERE request.status='pending' AND action.action_type<>$1)
	`, WorkReviewAction).Scan(&inputs, &reviews, &approvals)
	return
}
