package main

import (
	"slices"
	"strings"
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

func TestCopyleftDependencyIsRefused(t *testing.T) {
	for name, text := range map[string]string{
		"GPL":  "GNU GENERAL PUBLIC LICENSE Version 3, 29 June 2007",
		"AGPL": "GNU AFFERO GENERAL PUBLIC LICENSE Version 3",
		"SSPL": "Server Side Public License VERSION 1, OCTOBER 16, 2018",
		"BUSL": "Business Source License 1.1",
		"LGPL": "Permission is hereby granted, free of charge, to any person. Or the GNU Lesser General Public License, version 2.1.",
	} {
		_, err := buildPool([]component{{Name: "example.com/bad", Version: "v1.0.0", Files: []file{{Path: "LICENSE", Text: text}}}})
		if err == nil || !strings.Contains(err.Error(), "example.com/bad@v1.0.0") || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: error %v does not name the module and the licence", name, err)
		}
	}
}

func TestMPLNamingTheGPLFamilyIsAccepted(t *testing.T) {
	mpl := "Mozilla Public License Version 2.0. Secondary License means either the GNU General Public License, Version 2.0, the GNU Lesser General Public License, Version 2.1, the GNU Affero General Public License, Version 3.0."
	if _, err := buildPool([]component{{Name: "example.com/mpl", Version: "v1.0.0", Files: []file{{Path: "LICENSE", Text: mpl}}}}); err != nil {
		t.Errorf("MPL-2.0 refused: %v", err)
	}
}
