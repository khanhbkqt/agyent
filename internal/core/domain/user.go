package domain

import "time"

// User represents an authorized or recognized user.
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username,omitempty"`
	FullName  string    `json:"full_name,omitempty"`
	Role      string    `json:"role"` // e.g. "admin", "developer", "guest"
	CreatedAt time.Time `json:"created_at"`
}

// Group represents an authorized Telegram supergroup or group chat.
type Group struct {
	GroupID    string    `json:"group_id"`
	GroupTitle string    `json:"group_title,omitempty"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
}
