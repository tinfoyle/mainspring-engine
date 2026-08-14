package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type BusinessKnowledgeFact struct {
	Key         string
	Label       string
	Value       string
	Scope       string
	SourceType  string
	SourceRef   string
	Confidence  float64
	Sensitivity string
	ConfirmedAt *time.Time
	UpdatedAt   time.Time
}

type InputCoordinatorMessage struct {
	Role        string
	MessageKind string
	FactKey     string
	Body        string
	CreatedAt   time.Time
}

type InputCoordinatorTicket struct {
	ID     string
	Number int64
	Title  string
}

type InputCoordinatorQuestion struct {
	FactKey       string
	Label         string
	Prompt        string
	TicketCount   int
	QuestionCount int
	Tickets       []InputCoordinatorTicket
}

type InputCoordinatorState struct {
	Messages         []InputCoordinatorMessage
	Current          *InputCoordinatorQuestion
	PendingQuestions int
	PendingRequests  int
	RemainingTopics  int
	KnownFacts       int
	RecentFacts      []BusinessKnowledgeFact
}

type InputCoordinatorAnswerResult struct {
	Fact              BusinessKnowledgeFact
	AnsweredQuestions int
	ResolvedRequests  int
	Tickets           []InputCoordinatorTicket
}

func insertHumanInputQuestionLinks(ctx context.Context, tx pgx.Tx, requestID string, questions []string) error {
	requirements := make([]WorkInputRequirement, 0, len(questions))
	for _, question := range questions {
		requirements = append(requirements, WorkInputRequirement{FactKey: FactKeyForQuestion(question), Question: question})
	}
	return insertHumanInputRequirements(ctx, tx, requestID, requirements)
}

func insertHumanInputRequirements(ctx context.Context, tx pgx.Tx, requestID string, requirements []WorkInputRequirement) error {
	for index, requirement := range requirements {
		keySource := humanInputKeySource(requirement)
		if keySource == "heuristic" {
			requirement.FactKey = FactKeyForQuestion(requirement.Question)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO human_input_question_facts(request_id,question_index,fact_key,question,reason,key_source)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (request_id,question_index) DO NOTHING
		`, requestID, index, requirement.FactKey, requirement.Question, requirement.Reason, keySource); err != nil {
			return err
		}
	}
	return nil
}

func humanInputKeySource(requirement WorkInputRequirement) string {
	if requirement.FactKey == "" || (requirement.FactKey == FactKeyForQuestion(requirement.Question) && strings.TrimSpace(requirement.Reason) == "") {
		return "heuristic"
	}
	return "agent"
}

var factStopWords = map[string]bool{
	"a": true, "about": true, "and": true, "are": true, "as": true, "at": true, "be": true,
	"business": true, "can": true, "company": true, "could": true, "do": true, "does": true,
	"for": true, "from": true, "have": true, "how": true, "i": true, "in": true, "is": true,
	"it": true, "need": true, "of": true, "on": true, "or": true, "our": true, "please": true,
	"should": true, "that": true, "the": true, "their": true, "this": true, "to": true, "we": true,
	"what": true, "when": true, "where": true, "which": true, "who": true, "why": true, "with": true,
	"you": true, "your": true,
}

type factCategory struct {
	Key      string
	Label    string
	Prompt   string
	Keywords []string
}

var factCategories = []factCategory{
	{Key: "evidence.financial_records", Label: "Financial source records", Prompt: "Which accounting exports, bank or card statements, invoices, subscriptions, contracts, and vendor bills are available for this work? Attach what exists and identify anything that does not yet exist.", Keywords: []string{"upload or attach the available source records", "attach the available source records", "accounting exports", "bank statements", "financial source records"}},
	{Key: "organization.legal_name", Label: "Legal business name", Prompt: "What is the business's legal name, including any trade or DBA names it uses?", Keywords: []string{"legal name", "registered name", "dba", "trade name"}},
	{Key: "organization.entity_type", Label: "Legal entity", Prompt: "How is the business legally organized—for example, an LLC, corporation, partnership, or sole proprietorship?", Keywords: []string{"entity type", "legal entity", "incorporated", "corporation", "sole propriet", "partnership", " llc"}},
	{Key: "organization.primary_jurisdiction", Label: "Locations and jurisdictions", Prompt: "Where does the business operate, employ people, or serve customers? Include the relevant states, countries, or local jurisdictions.", Keywords: []string{"jurisdiction", "state does", "states do", "location", "located", "service area", "operate in", "registered in"}},
	{Key: "organization.industry", Label: "Industry and business model", Prompt: "How would you describe the business, its industry, and its business model?", Keywords: []string{"industry", "business model", "type of business", "kind of business"}},
	{Key: "organization.services", Label: "Products and services", Prompt: "What products or services does the business currently provide?", Keywords: []string{"products", "services", "offerings", "what do you sell", "what does the business do"}},
	{Key: "organization.website", Label: "Business website", Prompt: "What is the business's primary website or public domain?", Keywords: []string{"website", "domain name", "public site"}},
	{Key: "customers.primary_type", Label: "Customer profile", Prompt: "Who are the business's customers—businesses, consumers, government organizations, or a combination?", Keywords: []string{"customer type", "customers are", "serve businesses", "serve consumers", "target customer", "client type"}},
	{Key: "data.handling", Label: "Customer and sensitive data", Prompt: "What customer, employee, payment, health, or other sensitive data does the business receive, store, or access?", Keywords: []string{"personal data", "personal information", "customer data", "sensitive data", "payment data", "card data", "health data", "data do", "data does", "data access"}},
	{Key: "workforce.structure", Label: "Team and workforce", Prompt: "Who works in the business today? Include employees, owners, and contractors, plus any important roles or locations.", Keywords: []string{"employee", "contractor", "team size", "workforce", "staff", "how many people"}},
	{Key: "finance.accounting_system", Label: "Accounting system", Prompt: "Which accounting or bookkeeping system is authoritative for the business?", Keywords: []string{"accounting system", "bookkeeping system", "general ledger", "financial system", "books kept"}},
	{Key: "finance.reporting_period", Label: "Financial reporting period", Prompt: "Which reporting period or date range should the agents use for current financial work?", Keywords: []string{"reporting period", "fiscal year", "date range", "financial period"}},
	{Key: "risk.insurance", Label: "Insurance coverage", Prompt: "What business insurance policies are currently in force, and when do they renew?", Keywords: []string{"insurance", "policy coverage", "insured"}},
	{Key: "compliance.licenses", Label: "Licenses and permits", Prompt: "What licenses, registrations, or permits does the business currently hold, and which ones are still unknown or may need to be obtained?", Keywords: []string{"license", "licence", "permit", "registration required", "registered agent"}},
	{Key: "compliance.tax", Label: "Tax registrations", Prompt: "What tax registrations, filing jurisdictions, or tax accounts does the business currently have?", Keywords: []string{"tax registration", "sales tax", "payroll tax", "tax account", "tax filing"}},
	{Key: "security.access_owner", Label: "Security and access ownership", Prompt: "Who owns security and approves access to the business's systems or production environment?", Keywords: []string{"security owner", "owns security", "production access", "approv access", "access owner", "security responsibility"}},
	{Key: "operations.system_of_record", Label: "System of record", Prompt: "Which system should be treated as the authoritative source of record for this work?", Keywords: []string{"system of record", "authoritative system", "source of truth", "authoritative source"}},
}

var safeHeuristicFactCategories = map[string]bool{
	"evidence.financial_records":  true,
	"organization.legal_name":     true,
	"organization.entity_type":    true,
	"organization.website":        true,
	"finance.accounting_system":   true,
	"finance.reporting_period":    true,
	"risk.insurance":              true,
	"compliance.licenses":         true,
	"compliance.tax":              true,
	"security.access_owner":       true,
	"operations.system_of_record": true,
}

func FactKeyForQuestion(question string) string {
	normalized := normalizeFactText(question)
	for _, category := range factCategories {
		if !safeHeuristicFactCategories[category.Key] {
			continue
		}
		for _, keyword := range category.Keywords {
			if strings.Contains(" "+normalized+" ", strings.ToLower(keyword)) {
				return category.Key
			}
		}
	}
	tokens := strings.Fields(normalized)
	unique := map[string]bool{}
	compact := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if len(token) < 3 || factStopWords[token] || unique[token] {
			continue
		}
		if len(token) > 5 && strings.HasSuffix(token, "s") {
			token = strings.TrimSuffix(token, "s")
		}
		unique[token] = true
		compact = append(compact, token)
	}
	if len(compact) == 0 {
		compact = []string{normalized}
	}
	sort.Strings(compact)
	sum := sha256.Sum256([]byte(strings.Join(compact, "|")))
	return "owner." + hex.EncodeToString(sum[:6])
}

func normalizeFactText(value string) string {
	var output strings.Builder
	space := true
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			output.WriteRune(r)
			space = false
		} else if !space {
			output.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(output.String())
}

func factPresentation(key, fallbackQuestion string) (string, string) {
	for _, category := range factCategories {
		if category.Key == key {
			return category.Label, category.Prompt
		}
	}
	label := strings.TrimPrefix(key, "owner.")
	if strings.Contains(key, ".") {
		label = strings.ReplaceAll(key, ".", " ")
	}
	if label == "" || strings.HasPrefix(key, "owner.") {
		label = "Business context"
	}
	return strings.ToUpper(label[:1]) + label[1:], strings.TrimSpace(fallbackQuestion)
}

// reuseKnownHumanInputFacts only resolves requirements whose fact identity was
// explicitly supplied by an agent. Heuristic keys are useful for grouping
// equivalent owner questions, but are intentionally too broad to prove that an
// existing fact answers a newly worded question.
func reuseKnownHumanInputFacts(ctx context.Context, tx pgx.Tx, requestID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE human_input_question_facts link
		SET status='answered',answer=fact.value,
		    answered_at=COALESCE(link.answered_at,fact.confirmed_at,now()),updated_at=now()
		FROM business_knowledge_facts fact
		JOIN human_input_requests request ON request.status='pending'
		WHERE link.request_id=request.id AND link.fact_key=fact.fact_key
		  AND link.status='pending' AND link.key_source='agent' AND fact.status='active'
		  AND ($1='' OR link.request_id=$1::uuid)
	`, requestID)
	return err
}

func (s *ApprovalService) syncHumanInputKnowledge(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id::text,questions,response,status FROM human_input_requests ORDER BY requested_at`)
	if err != nil {
		return err
	}
	type requestSnapshot struct {
		id        string
		questions []string
		answers   []HumanInputAnswer
		status    string
	}
	var snapshots []requestSnapshot
	for rows.Next() {
		var snapshot requestSnapshot
		var questionsJSON, responseJSON []byte
		if err := rows.Scan(&snapshot.id, &questionsJSON, &responseJSON, &snapshot.status); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(questionsJSON, &snapshot.questions)
		_ = json.Unmarshal(responseJSON, &snapshot.answers)
		snapshots = append(snapshots, snapshot)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		for index, question := range snapshot.questions {
			key := FactKeyForQuestion(question)
			status, answer := "pending", ""
			if snapshot.status != "pending" && index < len(snapshot.answers) {
				status, answer = "answered", strings.TrimSpace(snapshot.answers[index].Answer)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO human_input_question_facts(request_id,question_index,fact_key,question,status,answer,answered_at,key_source)
				VALUES ($1,$2,$3,$4,$5,$6,CASE WHEN $5='answered' THEN now() END,'heuristic')
				ON CONFLICT (request_id,question_index) DO UPDATE
				SET fact_key=EXCLUDED.fact_key,question=EXCLUDED.question,updated_at=now()
				WHERE human_input_question_facts.status='pending' AND human_input_question_facts.key_source='heuristic'
			`, snapshot.id, index, key, question, status, answer); err != nil {
				return err
			}
			if status == "answered" && answer != "" {
				label, _ := factPresentation(key, question)
				if _, err := tx.Exec(ctx, `
					INSERT INTO business_knowledge_facts(fact_key,label,value,source_type,source_ref,confidence,confirmed_at)
					VALUES ($1,$2,$3,'owner',$4,1,now()) ON CONFLICT (fact_key) DO NOTHING
				`, key, label, answer, "human_input:"+snapshot.id); err != nil {
					return err
				}
			}
		}
	}
	if err := reuseKnownHumanInputFacts(ctx, tx, ""); err != nil {
		return err
	}
	if err := finalizeSatisfiedRequests(ctx, tx, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func finalizeSatisfiedRequests(ctx context.Context, tx pgx.Tx, documentIDs []string) error {
	documentsJSON, hasDocuments := humanInputDocumentsJSON(documentIDs)
	rows, err := tx.Query(ctx, `
		UPDATE human_input_requests request
		SET status='answered',
		    response=(SELECT jsonb_agg(jsonb_build_object('question',link.question,'answer',link.answer) ORDER BY link.question_index)
		              FROM human_input_question_facts link WHERE link.request_id=request.id),
		    response_document_ids=CASE WHEN $2::boolean THEN $1::jsonb ELSE request.response_document_ids END,
		    answered_at=COALESCE(request.answered_at,now())
		WHERE request.status='pending'
		  AND EXISTS (SELECT 1 FROM human_input_question_facts link WHERE link.request_id=request.id)
		  AND NOT EXISTS (SELECT 1 FROM human_input_question_facts link WHERE link.request_id=request.id AND link.status='pending')
		RETURNING request.work_item_id::text
	`, documentsJSON, hasDocuments)
	if err != nil {
		return err
	}
	var childIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range childIDs {
		if _, err := tx.Exec(ctx, `UPDATE work_items SET status='done',completed_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return nil
}

func humanInputDocumentsJSON(documentIDs []string) ([]byte, bool) {
	if documentIDs == nil {
		documentIDs = []string{}
	}
	documentsJSON, _ := json.Marshal(documentIDs)
	return documentsJSON, len(documentIDs) > 0
}

func (s *ApprovalService) InputCoordinatorState(ctx context.Context) (InputCoordinatorState, error) {
	if err := s.syncHumanInputKnowledge(ctx); err != nil {
		return InputCoordinatorState{}, err
	}
	var state InputCoordinatorState
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE link.status='pending'),
		       count(DISTINCT link.request_id) FILTER (WHERE link.status='pending'),
		       count(DISTINCT link.fact_key) FILTER (WHERE link.status='pending'),
		       (SELECT count(*) FROM business_knowledge_facts WHERE status='active')
		FROM human_input_question_facts link
		JOIN human_input_requests request ON request.id=link.request_id AND request.status='pending'
	`).Scan(&state.PendingQuestions, &state.PendingRequests, &state.RemainingTopics, &state.KnownFacts); err != nil {
		return InputCoordinatorState{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO input_coordinator_messages(role,message_kind,body)
		SELECT 'agent','intro',$1 WHERE NOT EXISTS (SELECT 1 FROM input_coordinator_messages WHERE message_kind='intro')
	`, coordinatorIntro(state.PendingQuestions, state.PendingRequests, state.RemainingTopics)); err != nil {
		return InputCoordinatorState{}, err
	}
	if state.PendingQuestions > 0 {
		question, err := s.nextCoordinatorQuestion(ctx)
		if err != nil {
			return InputCoordinatorState{}, err
		}
		state.Current = &question
		body := question.Prompt
		if question.TicketCount > 1 || question.QuestionCount > 1 {
			body += fmt.Sprintf("\n\nThis one answer can address %d open questions across %d tickets.", question.QuestionCount, question.TicketCount)
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO input_coordinator_messages(role,message_kind,fact_key,body,question_cycle)
			SELECT 'agent','question',$1,$2,
			       COALESCE((SELECT max(previous.question_cycle)+1
			                 FROM input_coordinator_messages previous
			                 WHERE previous.message_kind='question' AND previous.fact_key=$1),0)
			WHERE NOT EXISTS (
				SELECT 1 FROM input_coordinator_messages active
				WHERE active.message_kind='question' AND active.fact_key=$1
				  AND NOT EXISTS (
					SELECT 1 FROM input_coordinator_messages answered
					WHERE answered.fact_key=$1
					  AND answered.message_kind IN ('answer','progress')
					  AND answered.created_at > active.created_at
				  )
			)
			ON CONFLICT DO NOTHING
		`, question.FactKey, body); err != nil {
			return InputCoordinatorState{}, err
		}
	} else {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO input_coordinator_messages(role,message_kind,body)
			SELECT 'agent','complete','We have worked through every open owner question. The unblocked agents can now continue their tickets.'
			WHERE EXISTS (SELECT 1 FROM human_input_question_facts)
			  AND NOT EXISTS (SELECT 1 FROM input_coordinator_messages WHERE message_kind='complete')
		`); err != nil {
			return InputCoordinatorState{}, err
		}
	}
	factRows, err := s.pool.Query(ctx, `
		SELECT fact_key,label,value,scope,source_type,source_ref,confidence::float8,sensitivity,confirmed_at,updated_at
		FROM business_knowledge_facts WHERE status='active' ORDER BY updated_at DESC,fact_key LIMIT 12
	`)
	if err != nil {
		return InputCoordinatorState{}, err
	}
	for factRows.Next() {
		var fact BusinessKnowledgeFact
		if err := factRows.Scan(&fact.Key, &fact.Label, &fact.Value, &fact.Scope, &fact.SourceType, &fact.SourceRef, &fact.Confidence, &fact.Sensitivity, &fact.ConfirmedAt, &fact.UpdatedAt); err != nil {
			factRows.Close()
			return InputCoordinatorState{}, err
		}
		state.RecentFacts = append(state.RecentFacts, fact)
	}
	factRows.Close()
	if err := factRows.Err(); err != nil {
		return InputCoordinatorState{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT role,message_kind,fact_key,body,created_at FROM input_coordinator_messages ORDER BY created_at,id`)
	if err != nil {
		return InputCoordinatorState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var message InputCoordinatorMessage
		if err := rows.Scan(&message.Role, &message.MessageKind, &message.FactKey, &message.Body, &message.CreatedAt); err != nil {
			return InputCoordinatorState{}, err
		}
		state.Messages = append(state.Messages, message)
	}
	return state, rows.Err()
}

func coordinatorIntro(questions, requests, topics int) string {
	if questions == 0 {
		return "I will coordinate questions from your agents here. When several tickets need the same fact, I will ask once and share the answer with all of them."
	}
	return fmt.Sprintf("I reviewed %d open questions from %d tickets and consolidated them into %d business topics. I will start with the answers that unblock the most work.", questions, requests, topics)
}

func (s *ApprovalService) nextCoordinatorQuestion(ctx context.Context) (InputCoordinatorQuestion, error) {
	var result InputCoordinatorQuestion
	if err := s.pool.QueryRow(ctx, `
		SELECT link.fact_key,count(*),count(DISTINCT link.request_id)
		FROM human_input_question_facts link
		JOIN human_input_requests request ON request.id=link.request_id AND request.status='pending'
		WHERE link.status='pending'
		GROUP BY link.fact_key
		ORDER BY count(DISTINCT link.request_id) DESC,
		         CASE WHEN link.fact_key LIKE 'compliance.%' OR link.fact_key LIKE 'security.%' OR link.fact_key LIKE 'finance.%' THEN 0 ELSE 1 END,
		         count(*) DESC,min(link.created_at)
		LIMIT 1
	`).Scan(&result.FactKey, &result.QuestionCount, &result.TicketCount); err != nil {
		return InputCoordinatorQuestion{}, err
	}
	questionRows, err := s.pool.Query(ctx, `
		SELECT link.question
		FROM human_input_question_facts link
		JOIN human_input_requests request ON request.id=link.request_id AND request.status='pending'
		WHERE link.status='pending' AND link.fact_key=$1
		GROUP BY link.question
		ORDER BY min(link.created_at),link.question
	`, result.FactKey)
	if err != nil {
		return InputCoordinatorQuestion{}, err
	}
	var questions []string
	for questionRows.Next() {
		var question string
		if err := questionRows.Scan(&question); err != nil {
			questionRows.Close()
			return InputCoordinatorQuestion{}, err
		}
		questions = append(questions, question)
	}
	questionRows.Close()
	if err := questionRows.Err(); err != nil {
		return InputCoordinatorQuestion{}, err
	}
	var known bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM business_knowledge_facts WHERE fact_key=$1 AND status='active'
	)`, result.FactKey).Scan(&known); err != nil {
		return InputCoordinatorQuestion{}, err
	}
	result.Label, result.Prompt = coordinatorQuestionPresentation(result.FactKey, questions, known)
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT parent.id::text,parent.number,parent.title
		FROM human_input_question_facts link
		JOIN human_input_requests request ON request.id=link.request_id AND request.status='pending'
		JOIN work_items parent ON parent.id=request.parent_work_item_id
		WHERE link.status='pending' AND link.fact_key=$1
		ORDER BY parent.number
	`, result.FactKey)
	if err != nil {
		return InputCoordinatorQuestion{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var ticket InputCoordinatorTicket
		if err := rows.Scan(&ticket.ID, &ticket.Number, &ticket.Title); err != nil {
			return InputCoordinatorQuestion{}, err
		}
		result.Tickets = append(result.Tickets, ticket)
	}
	return result, rows.Err()
}

func coordinatorQuestionPresentation(factKey string, questions []string, known bool) (string, string) {
	fallback := ""
	if len(questions) > 0 {
		fallback = strings.TrimSpace(questions[0])
	}
	label, canonical := factPresentation(factKey, fallback)
	canonical = strings.TrimSpace(canonical)
	if len(questions) == 0 {
		return label, canonical
	}
	if len(questions) == 1 {
		return label, fallback
	}
	// Once a business fact is known, a new request for the same topic is a
	// follow-up rather than a request to repeat the generic baseline question.
	// Unknown/custom fact keys also have no safe canonical prompt, so preserve
	// every distinct question that the tickets actually asked.
	if known || canonical == fallback {
		var prompt strings.Builder
		prompt.WriteString("Please address these related follow-ups:")
		for index, question := range questions {
			fmt.Fprintf(&prompt, "\n\n%d. %s", index+1, strings.TrimSpace(question))
		}
		return label, prompt.String()
	}
	return label, canonical
}

func (s *ApprovalService) AnswerInputCoordinator(ctx context.Context, factKey, userID, answer string, documentIDs []string) (InputCoordinatorAnswerResult, error) {
	factKey, answer = strings.TrimSpace(factKey), strings.TrimSpace(answer)
	if factKey == "" || len([]rune(answer)) == 0 || len([]rune(answer)) > 12000 {
		return InputCoordinatorAnswerResult{}, errors.New("an answer must contain between 1 and 12,000 characters")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return InputCoordinatorAnswerResult{}, errors.New("answering user is invalid")
	}
	if err := s.syncHumanInputKnowledge(ctx); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT link.request_id::text,link.question,parent.id::text,parent.number,parent.title
		FROM human_input_question_facts link
		JOIN human_input_requests request ON request.id=link.request_id AND request.status='pending'
		JOIN work_items parent ON parent.id=request.parent_work_item_id
		WHERE link.fact_key=$1 AND link.status='pending'
		ORDER BY parent.number,link.question_index FOR UPDATE OF link
	`, factKey)
	if err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	requestIDs := map[string]bool{}
	ticketIDs := map[string]bool{}
	var tickets []InputCoordinatorTicket
	answeredQuestions := 0
	var fallbackQuestion string
	for rows.Next() {
		var requestID, question string
		var ticket InputCoordinatorTicket
		if err := rows.Scan(&requestID, &question, &ticket.ID, &ticket.Number, &ticket.Title); err != nil {
			rows.Close()
			return InputCoordinatorAnswerResult{}, err
		}
		if fallbackQuestion == "" {
			fallbackQuestion = question
		}
		requestIDs[requestID] = true
		answeredQuestions++
		if !ticketIDs[ticket.ID] {
			ticketIDs[ticket.ID] = true
			tickets = append(tickets, ticket)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	if answeredQuestions == 0 {
		return InputCoordinatorAnswerResult{}, errors.New("this coordinator question is no longer pending")
	}
	label, _ := factPresentation(factKey, fallbackQuestion)
	if _, err := tx.Exec(ctx, `
		INSERT INTO business_knowledge_facts(fact_key,label,value,source_type,source_ref,confidence,confirmed_at)
		VALUES ($1,$2,$3,'owner',$4,1,now())
		ON CONFLICT (fact_key) DO UPDATE SET label=EXCLUDED.label,value=EXCLUDED.value,source_type='owner',
		    source_ref=EXCLUDED.source_ref,confidence=1,status='active',confirmed_at=now(),updated_at=now()
	`, factKey, label, answer, "input_coordinator:"+userID); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE human_input_question_facts SET status='answered',answer=$2,answered_at=now(),updated_at=now()
		WHERE fact_key=$1 AND status='pending' AND request_id IN (SELECT id FROM human_input_requests WHERE status='pending')
	`, factKey, answer); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO input_coordinator_messages(role,message_kind,fact_key,body) VALUES ('user','answer',$1,$2)`, factKey, answer); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	before := 0
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM human_input_requests WHERE status='pending' AND id=ANY($1::uuid[])`, mapKeys(requestIDs)).Scan(&before); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	if err := finalizeSatisfiedRequests(ctx, tx, documentIDs); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	after := 0
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM human_input_requests WHERE status='pending' AND id=ANY($1::uuid[])`, mapKeys(requestIDs)).Scan(&after); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	resolved := before - after
	progress := fmt.Sprintf("I saved that as %s and applied it to %d open question", strings.ToLower(label), answeredQuestions)
	if answeredQuestions != 1 {
		progress += "s"
	}
	if resolved > 0 {
		progress += fmt.Sprintf(". %d ticket request", resolved)
		if resolved != 1 {
			progress += "s are"
		} else {
			progress += " is"
		}
		progress += " now complete, so the assigned agents can continue"
	}
	progress += "."
	if _, err := tx.Exec(ctx, `INSERT INTO input_coordinator_messages(role,message_kind,fact_key,body) VALUES ('agent','progress',$1,$2)`, factKey, progress); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InputCoordinatorAnswerResult{}, err
	}
	return InputCoordinatorAnswerResult{
		Fact:              BusinessKnowledgeFact{Key: factKey, Label: label, Value: answer, Scope: "company", SourceType: "owner", Confidence: 1},
		AnsweredQuestions: answeredQuestions, ResolvedRequests: resolved, Tickets: tickets,
	}, nil
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func (s *ApprovalService) ListBusinessKnowledge(ctx context.Context) ([]BusinessKnowledgeFact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT fact_key,label,value,scope,source_type,source_ref,confidence::float8,sensitivity,confirmed_at,updated_at
		FROM business_knowledge_facts WHERE status='active' ORDER BY label,fact_key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []BusinessKnowledgeFact
	for rows.Next() {
		var fact BusinessKnowledgeFact
		if err := rows.Scan(&fact.Key, &fact.Label, &fact.Value, &fact.Scope, &fact.SourceType, &fact.SourceRef, &fact.Confidence, &fact.Sensitivity, &fact.ConfirmedAt, &fact.UpdatedAt); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}
