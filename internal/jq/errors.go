package jq

import "errors"

// errTrailingData reports extra data after the single JSON document.
var errTrailingData = errors.New("unexpected trailing data")

// errWantObject reports a document that is not a JSON object.
var errWantObject = errors.New("want a JSON object")

// errWantArray reports a document that is not a JSON array.
var errWantArray = errors.New("want a JSON array")
