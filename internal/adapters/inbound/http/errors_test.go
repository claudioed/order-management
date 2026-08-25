package http_test

import "errors"

// errUnmapped is an error the adapter's status/problem tables do not know
// about — the default 500 path.
var errUnmapped = errors.New("something the adapter has never heard of")
