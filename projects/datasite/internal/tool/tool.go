package tool

// import all the go tools here, so Gazelle pulls in their transitive dependencies

import (
	_ "github.com/a-h/templ"
	_ "github.com/ogen-go/ogen"
	_ "github.com/sqlc-dev/sqlc"
)
