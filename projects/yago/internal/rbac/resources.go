package rbac

import (
	"context"

	"github.com/piperswe/Codebase/projects/yago/internal/db"
)

type Resource string

const (
	RESOURCE_USERS       Resource = "users"
	RESOURCE_USER_SELF   Resource = "user_self"
	RESOURCE_ROLES       Resource = "roles"
	RESOURCE_PERMISSIONS Resource = "permissions"
)

// CanRead returns a list of permissions that would allow reading the resource.
func (r *Resource) CanRead() []Permission {
	return []Permission{
		{
			Resource: *r,
			Action:   ACTION_ADMIN,
		},
		{
			Resource: *r,
			Action:   ACTION_READ,
		},
		{
			Resource: *r,
			Action:   ACTION_WRITE,
		},
	}
}

func (r *Resource) UserCanRead(ctx context.Context, q db.Queries, userID int64) (bool, error) {
	canRead := r.CanRead()
	permissions := make([]string, len(canRead))
	for i, p := range canRead {
		permissions[i] = p.String()
	}
	return q.UserHasOneOfPermissions(ctx, db.UserHasOneOfPermissionsParams{
		UserID:      userID,
		Permissions: permissions,
	})
}

// CanWrite returns a list of permissions that would allow writing the resource.
func (r *Resource) CanWrite() []Permission {
	return []Permission{
		{
			Resource: *r,
			Action:   ACTION_ADMIN,
		},
		{
			Resource: *r,
			Action:   ACTION_WRITE,
		},
	}
}

func (r *Resource) UserCanWrite(ctx context.Context, q db.Queries, userID int64) (bool, error) {
	canWrite := r.CanWrite()
	permissions := make([]string, len(canWrite))
	for i, p := range canWrite {
		permissions[i] = p.String()
	}
	return q.UserHasOneOfPermissions(ctx, db.UserHasOneOfPermissionsParams{
		UserID:      userID,
		Permissions: permissions,
	})
}
