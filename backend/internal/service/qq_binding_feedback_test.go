package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func expectQQFeedbackSend(m sqlmock.Sqlmock, pending bool, key, state string) {
	query := "SELECT qq"
	args := []any{int64(42)}
	if pending {
		query = "SELECT n.pending_qq"
		args = append(args, qqHash("0123456789ABCDEF"))
	}
	recipient := func() {
		if pending {
			m.ExpectQuery(query).WithArgs(int64(42), args[1]).WillReturnRows(sqlmock.NewRows([]string{"qq"}).AddRow("234567"))
		} else {
			m.ExpectQuery(query).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"qq"}).AddRow("234567"))
		}
	}
	m.ExpectBegin()
	recipient()
	m.ExpectExec("INSERT INTO qq_delivery_attempts").WithArgs(key, int64(42), "234567").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectBegin()
	recipient()
	m.ExpectQuery("SELECT EXISTS").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	m.ExpectCommit()
	id := "123"
	if state != "sent" {
		id = ""
	}
	m.ExpectExec("UPDATE qq_delivery_attempts").WithArgs(key, state, id).WillReturnResult(sqlmock.NewResult(0, 1))
}
func TestQQAcceptedCodeRepliesOnlyAfterPendingStored(t *testing.T) {
	s, m := qqTestService(t)
	var message string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		message = p.Message
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":123}}`))
	}))
	defer server.Close()
	s.endpoint = server.URL
	m.ExpectQuery("SELECT n.user_id").WithArgs("234567").WillReturnError(sql.ErrNoRows)
	m.ExpectBegin()
	m.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.ExpectExec("INSERT INTO qq_inbound_events").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectExec("UPDATE qq_user_notifications SET pending_qq").WithArgs(qqHash("0123456789ABCDEF"), "234567").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery("SELECT user_id").WithArgs(qqHash("0123456789ABCDEF"), "234567").WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(42))
	expectQQFeedbackSend(m, true, "binding-pending:42:"+qqHash("0123456789ABCDEF"), "sent")
	ev := QQEvent{SelfID: "123456", UserID: "234567", MessageID: "14", Time: time.Now().Unix(), PostType: "message", MessageType: "private", SubType: "friend", Message: json.RawMessage(`[{"type":"text","data":{"text":"0123456789ABCDEF"}}]`)}
	body, _ := json.Marshal(ev)
	if err := s.Webhook(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "绑定码正确") || !strings.Contains(message, "确认绑定") || strings.Contains(message, "绑定成功！") {
		t.Fatal(message)
	}
}
func TestQQPendingReplyRechecksCurrentChallengeBeforeSending(t *testing.T) {
	s, m := qqTestService(t)
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer server.Close()
	s.endpoint = server.URL
	m.ExpectBegin()
	m.ExpectQuery("SELECT n.pending_qq").WillReturnRows(sqlmock.NewRows([]string{"qq"}).AddRow("234567"))
	m.ExpectExec("INSERT INTO qq_delivery_attempts").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectBegin()
	m.ExpectQuery("SELECT n.pending_qq").WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	if s.deliver(context.Background(), 42, "234567", "pending", "reply", false, "binding-pending", "old") == nil || hits != 0 {
		t.Fatal("sent after challenge changed")
	}
}
func TestQQPendingUnknownReplyNeverResends(t *testing.T) {
	s, m := qqTestService(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT n.pending_qq").WillReturnRows(sqlmock.NewRows([]string{"qq"}).AddRow("234567"))
	m.ExpectExec("INSERT INTO qq_delivery_attempts").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("SELECT state").WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("unknown"))
	m.ExpectRollback()
	if s.deliver(context.Background(), 42, "234567", "pending", "reply", false, "binding-pending", "hash") == nil {
		t.Fatal("unknown accepted")
	}
}
func TestQQConfirmSuccessReplyDoesNotDependOnNotificationPreferences(t *testing.T) {
	s, m := qqTestService(t)
	var message string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		message = p.Message
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"message_id":123}}`))
	}))
	defer server.Close()
	s.endpoint = server.URL
	m.ExpectBegin()
	m.ExpectQuery("SELECT pending_qq").WithArgs(int64(42), "challenge").WillReturnRows(sqlmock.NewRows([]string{"pending_qq"}).AddRow("234567"))
	m.ExpectExec("UPDATE qq_user_notifications SET qq").WithArgs(int64(42), "234567").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	expectQQFeedbackSend(m, false, "binding-confirmed:42:challenge", "sent")
	if err := s.Confirm(context.Background(), 42, "23****67", "challenge"); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"绑定成功", "普通对话", "查余额", "通知偏好", "指定开通"} {
		if !strings.Contains(message, part) {
			t.Fatal(message)
		}
	}
}
func TestQQCommittedBindingRemainsSuccessfulWhenReplyFails(t *testing.T) {
	s, m := qqTestService(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT pending_qq").WillReturnRows(sqlmock.NewRows([]string{"pending_qq"}).AddRow("234567"))
	m.ExpectExec("UPDATE qq_user_notifications SET qq").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectBegin()
	m.ExpectQuery("SELECT qq").WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	if err := s.Confirm(context.Background(), 42, "23****67", "challenge"); err != nil {
		t.Fatal("committed binding returned retryable error", err)
	}
}

func TestQQPendingReplyRejectsDifferentQQ(t *testing.T) {
	s, m := qqTestService(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT n.pending_qq").WillReturnRows(sqlmock.NewRows([]string{"qq"}).AddRow("345678"))
	m.ExpectRollback()
	if s.deliver(context.Background(), 42, "234567", "pending", "reply", false, "binding-pending", "hash") == nil {
		t.Fatal("different QQ inherited pending permission")
	}
}
