package services

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// recordingNotificationManager keeps the notifications NotificationService
// stores, so tests can assert recipients and targets.
type recordingNotificationManager struct {
	mu    sync.Mutex
	added []models.Notification
}

func (m *recordingNotificationManager) AddNotification(n models.Notification) (models.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, n)
	return n, nil
}

func (m *recordingNotificationManager) AddNotifications(ns []models.Notification) ([]models.Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, ns...)
	return ns, nil
}

func (m *recordingNotificationManager) AddNotificationsContext(_ context.Context, ns []models.Notification) ([]models.Notification, error) {
	return m.AddNotifications(ns)
}

func (m *recordingNotificationManager) DeleteUserNotifications(int) error       { return nil }
func (m *recordingNotificationManager) MarkNotificationsSent([]int) error       { return nil }
func (m *recordingNotificationManager) MarkNotificationsSendFailed([]int) error { return nil }
func (m *recordingNotificationManager) RollbackNotificationsSent([]int) error   { return nil }

// take returns and clears the recorded notifications.
func (m *recordingNotificationManager) take() []models.Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	added := m.added
	m.added = nil
	return added
}

// perUserMilestoneAccess grants milestone read to the listed viewers only.
type perUserMilestoneAccess struct {
	milestones map[int]MilestoneResult
	viewers    map[int]map[int]bool // milestone -> user -> may view
}

func (f perUserMilestoneAccess) AuthorizeMilestoneRead(userID, milestoneID int) (*MilestoneResult, error) {
	milestone, ok := f.milestones[milestoneID]
	if !ok || !f.viewers[milestoneID][userID] {
		return nil, ErrPlanningForbidden
	}
	return &milestone, nil
}

const (
	mentionAlice = 1 // comment author
	mentionBob   = 2 // can view both milestones
	mentionCarol = 3 // can't view the workspace milestone
	mentionDave  = 4 // can view both milestones

	mentionWorkspaceID        = 5
	mentionWorkspaceMilestone = 20
	mentionGlobalMilestone    = 21
)

func newMilestoneMentionTestService(t *testing.T) (*MilestoneCommentService, *recordingNotificationManager) {
	t.Helper()
	db, err := database.NewSQLiteDBWithPoolSizes(filepath.Join(t.TempDir(), "windshift.db"), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO users (id, email, username, first_name, last_name) VALUES
		(1, 'alice@example.test', 'alice', 'Alice', 'Author'),
		(2, 'bob@example.test', 'bob', 'Bob', 'Builder'),
		(3, 'carol@example.test', 'carol', 'Carol', 'Outsider'),
		(4, 'dave@example.test', 'dave', 'Dave', 'Viewer')`); err != nil {
		t.Fatal(err)
	}

	manager := &recordingNotificationManager{}
	notifications := NewNotificationService(db, manager, nil, NotificationServiceConfig{})
	t.Cleanup(func() { _ = notifications.Close() })
	mentions := NewMentionService(db, notifications, nil)

	workspaceID := mentionWorkspaceID
	access := perUserMilestoneAccess{
		milestones: map[int]MilestoneResult{
			mentionWorkspaceMilestone: {ID: mentionWorkspaceMilestone, Name: "Beta", WorkspaceID: &workspaceID},
			mentionGlobalMilestone:    {ID: mentionGlobalMilestone, Name: "Launch", IsGlobal: true},
		},
		viewers: map[int]map[int]bool{
			mentionWorkspaceMilestone: {mentionAlice: true, mentionBob: true, mentionDave: true},
			mentionGlobalMilestone:    {mentionAlice: true, mentionBob: true, mentionCarol: true, mentionDave: true},
		},
	}
	service := NewMilestoneCommentService(newFakeMilestoneCommentStore(), access)
	service.SetMentionNotifier(mentions)
	return service, manager
}

func notifiedUsers(notifications []models.Notification) []int {
	ids := make([]int, 0, len(notifications))
	for _, n := range notifications {
		ids = append(ids, n.UserID)
	}
	sort.Ints(ids)
	return ids
}

func sameUsers(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestMilestoneCommentMentionNotifiesViewersOnce(t *testing.T) {
	service, manager := newMilestoneMentionTestService(t)

	// Bob twice (username and display name), Carol can't view, Alice is the author.
	if _, err := service.Create(mentionAlice, mentionWorkspaceMilestone, `Ping @bob and @"Bob Builder", also @carol and @alice`); err != nil {
		t.Fatal(err)
	}
	got := manager.take()
	if !sameUsers(notifiedUsers(got), []int{mentionBob}) {
		t.Fatalf("expected only Bob notified, got %+v", got)
	}
	n := got[0]
	if n.Type != "mention" || n.ActionURL != "/milestones/20?workspaceId=5" {
		t.Fatalf("expected a mention opening the milestone, got type %q url %q", n.Type, n.ActionURL)
	}
	if n.AuthorizationScope != models.NotificationScopeWorkspace || n.WorkspaceID == nil || *n.WorkspaceID != mentionWorkspaceID || n.ItemID != nil {
		t.Fatalf("expected workspace-scoped provenance without an item, got %+v", n)
	}
	if n.SourceType != "milestone" || n.SourceID == nil || *n.SourceID != mentionWorkspaceMilestone {
		t.Fatalf("expected the milestone as source, got %q %v", n.SourceType, n.SourceID)
	}
	if !strings.Contains(n.Message, "Alice Author") || !strings.Contains(n.Message, "Beta") {
		t.Fatalf("expected actor and milestone in message, got %q", n.Message)
	}
}

func TestMilestoneCommentMentionOnGlobalMilestone(t *testing.T) {
	service, manager := newMilestoneMentionTestService(t)

	if _, err := service.Create(mentionAlice, mentionGlobalMilestone, "@carol please look"); err != nil {
		t.Fatal(err)
	}
	got := manager.take()
	if !sameUsers(notifiedUsers(got), []int{mentionCarol}) {
		t.Fatalf("expected Carol notified on the global milestone, got %+v", got)
	}
	n := got[0]
	if n.ActionURL != "/milestones/21" || n.AuthorizationScope != models.NotificationScopeSystem || n.WorkspaceID != nil {
		t.Fatalf("expected a system-scoped link to the global milestone, got %+v", n)
	}
}

func TestMilestoneCommentEditNotifiesOnlyNewMentions(t *testing.T) {
	service, manager := newMilestoneMentionTestService(t)

	created, err := service.Create(mentionAlice, mentionWorkspaceMilestone, "@bob first")
	if err != nil {
		t.Fatal(err)
	}
	manager.take()

	steps := []struct {
		content string
		want    []int
	}{
		{"@bob first, @dave too", []int{mentionDave}},    // only the added mention
		{"@bob first, @dave too!", nil},                  // nothing new
		{"@dave only", nil},                              // dropping a mention notifies nobody
		{"@dave and @bob again", []int{mentionBob}},      // re-added mention notifies again
		{"@dave and @bob again, @carol", nil},            // Carol can't view the milestone
		{"@dave and @bob again, @carol, @alice me", nil}, // self-mention
	}
	for _, step := range steps {
		if _, err := service.Update(mentionAlice, mentionWorkspaceMilestone, created.ID, step.content); err != nil {
			t.Fatal(err)
		}
		if got := notifiedUsers(manager.take()); !sameUsers(got, step.want) {
			t.Fatalf("edit to %q: expected %v notified, got %v", step.content, step.want, got)
		}
	}
}

func TestMilestoneCommentWithoutMentionsNotifiesNobody(t *testing.T) {
	service, manager := newMilestoneMentionTestService(t)

	if _, err := service.Create(mentionAlice, mentionWorkspaceMilestone, "mail bob@example.test or @nobody"); err != nil {
		t.Fatal(err)
	}
	if got := manager.take(); len(got) != 0 {
		t.Fatalf("expected no notifications, got %+v", got)
	}
}
