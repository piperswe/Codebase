package rbac

type Permission struct {
	Resource Resource
	Action   Action
}

func (p *Permission) String() string {
	return string(p.Resource) + ":" + string(p.Action)
}
