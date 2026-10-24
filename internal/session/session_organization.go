package session

import (
	"time"
)

type OrganizationGroup struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SessionOrganization struct {
	Folders []OrganizationGroup `json:"folders"`
}
