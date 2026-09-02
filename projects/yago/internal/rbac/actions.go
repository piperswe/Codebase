package rbac

type Action string

const (
	ACTION_ADMIN Action = "admin"
	ACTION_WRITE Action = "write"
	ACTION_READ  Action = "read"
)
