package model

import "time"

// Account identifies a user independently of their mutable login.
type Account struct {
	ID    string
	Login string
	Host  string
}

// Key returns the persistent namespace shared by all sessions of this account.
func (a Account) Key() string {
	return a.Host + "/" + a.ID
}

// PR is a complete observed revision; Complete is false when a source is missing.
type PR struct {
	ID           string
	Repository   string
	Number       int
	Url          string
	Title        string
	State        string
	Draft        bool
	HeadSHA      string
	MergeSHA     string
	HeadRepo     string
	HeadRef      string
	ReviewStatus string
	CIStatus     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastCI       time.Time
	LastReview   time.Time
	LastMessage  time.Time
	Complete     bool
	Checks       []Check
	Runs         []Run
	Comments     []Comment
	Reviews      []Review
}

// Check describes the latest observed execution of a check or status context.
type Check struct {
	ID          string
	Name        string
	App         string
	Revision    string
	State       string
	Conclusion  string
	Url         string
	RunID       int64
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

// Run identifies a GitHub Actions execution and its latest attempt.
type Run struct {
	ID         int64
	WorkflowID int64
	Attempt    int
	Name       string
	Event      string
	HeadSHA    string
	State      string
	Conclusion string
	Url        string
	CreatedAt  time.Time
	StartedAt  time.Time
	UpdatedAt  time.Time
}

// Comment carries identity and thread membership without retaining its body.
type Comment struct {
	ID        string
	Author    string
	Url       string
	ThreadID  string
	Reply     bool
	CreatedAt time.Time
}

// Review represents a published review, including a dismissed review's identity.
type Review struct {
	ID          string
	Author      string
	State       string
	Url         string
	SubmittedAt time.Time
}

// Record preserves acknowledgement separately from the current PR status.
type Record struct {
	PR            PR
	Sequence      uint64
	Seen          uint64
	ManualUnread  bool
	Unread        map[string]int
	CICandidate   string
	CICandidateAt time.Time
	CINotified    string
	CIInitialized bool
}

// UnreadCount returns the number of unacknowledged events, including a manual mark.
func (r Record) UnreadCount() int {
	count := 0
	for _, value := range r.Unread {
		count += value
	}

	if r.ManualUnread && count == 0 {
		return 1
	}

	return count
}

// AccountState is committed together with notifications derived from its update.
type AccountState struct {
	Initialized bool
	Since       time.Time
	Scope       []string
	Records     map[string]Record
}
