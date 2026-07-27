package server

import (
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/notifications"
	"github.com/schneik80/fusionlocalserver/tasks"
)

// seedAssignedTask creates a task in project p assigned to the given user with
// the given due date, and returns the stores wired into a notifCtx.
func reminderCtx(t *testing.T, dueDate string) (*Server, notifCtx) {
	t.Helper()
	ts, err := tasks.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("tasks store: %v", err)
	}
	ns, err := notifications.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("notifications store: %v", err)
	}
	const user = "sub-assignee"
	if _, err := ts.Create("proj:1", "hub:1", "Proj One", tasks.Draft{
		Title:    "Fixture rev B",
		DueDate:  dueDate,
		Assignee: &tasks.UserRef{ID: user, Name: "Ada"},
	}, tasks.UserRef{ID: "sub-creator"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	s := &Server{logger: quietLogger()}
	return s, notifCtx{userKey: user, userID: user, store: ns, tasks: ts}
}

func TestReconcileEmitsOverdue(t *testing.T) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	s, c := reminderCtx(t, yesterday)

	s.reconcileTaskReminders(c)
	list, _ := c.store.List(c.userKey, 0)
	if len(list) != 1 || list[0].Kind != notifications.KindOverdue {
		t.Fatalf("want one overdue reminder, got %+v", list)
	}
	if list[0].Subject != "Fixture rev B" || list[0].ProjectName != "Proj One" {
		t.Fatalf("reminder missing captured context: %+v", list[0])
	}

	// Idempotent: a second reconcile must not double-emit (dedupe key).
	s.reconcileTaskReminders(c)
	if list, _ := c.store.List(c.userKey, 0); len(list) != 1 {
		t.Fatalf("reconcile re-emitted: %d entries", len(list))
	}
}

func TestReconcileEmitsDueSoon(t *testing.T) {
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	s, c := reminderCtx(t, tomorrow)
	s.reconcileTaskReminders(c)
	list, _ := c.store.List(c.userKey, 0)
	if len(list) != 1 || list[0].Kind != notifications.KindDueSoon {
		t.Fatalf("want one due-soon reminder, got %+v", list)
	}
}

func TestReconcileIgnoresFarFuture(t *testing.T) {
	far := time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02")
	s, c := reminderCtx(t, far)
	s.reconcileTaskReminders(c)
	if list, _ := c.store.List(c.userKey, 0); len(list) != 0 {
		t.Fatalf("far-future due date should not remind, got %+v", list)
	}
}

func TestReconcileIgnoresTasksNotAssignedToMe(t *testing.T) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	s, c := reminderCtx(t, yesterday)
	// Reconcile for a DIFFERENT user: the task is assigned to sub-assignee, so
	// this user's inbox stays empty.
	c.userKey = "sub-other"
	c.userID = "sub-other"
	s.reconcileTaskReminders(c)
	if list, _ := c.store.List("sub-other", 0); len(list) != 0 {
		t.Fatalf("reminder leaked to a non-assignee: %+v", list)
	}
}
