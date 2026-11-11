package main

import (
	"slices"
	"testing"
)

func TestSplitSeparatesCopyrightFromText(t *testing.T) {
	mit := "The MIT License (MIT)\n\nCopyright (c) 2020 A\nAll rights reserved.\n\nPermission is hereby granted, free of charge, to any person.\n(c) not a copyright line\n"
	words, text, cp := split(mit)
	if want := []string{"Copyright (c) 2020 A All rights reserved."}; !slices.Equal(cp, want) {
		t.Errorf("copyright lines = %q, want %q", cp, want)
	}
	if label(words) != "MIT" {
		t.Errorf("label = %q, want MIT", label(words))
	}
	if canon(words) != "Permission is hereby granted, free of charge, to any person. (c) not a copyright line" {
		t.Errorf("canon = %q", canon(words))
	}
	if text == "" || text[0] == 'C' {
		t.Errorf("text %q still holds the copyright line", text)
	}
}

func TestCanonIgnoresTheApacheAppendix(t *testing.T) {
	body := "Apache License Version 2.0 TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION 1. Definitions."
	withAppendix := body + " END OF TERMS AND CONDITIONS APPENDIX: How to apply the Apache License to your work. Licensed under the Apache License, Version 2.0 (the \"License\"); you may not. limitations under the License."
	if canon(body) != canon(withAppendix) {
		t.Errorf("appendix changed the key:\n%q\n%q", canon(body), canon(withAppendix))
	}
}
