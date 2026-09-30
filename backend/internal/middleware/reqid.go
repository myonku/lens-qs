package middleware

import "github.com/myonku/lens-qs-api/internal/pkg/id"

func newRequestID() string { return id.New() }
