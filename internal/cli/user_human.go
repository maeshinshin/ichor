/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"io"
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// kindUser is the word of the user result lines.
const kindUser = "user"

// The state column of `ichor user list`.
const (
	userActive     = "active"
	userDisabled   = "disabled"
	userNoPassword = "no password"
)

// userState is what stops a user from signing in, else "active".
func userState(u *pb.User) string {
	switch {
	case u.GetDisabled():
		return userDisabled
	case !u.GetPasswordSet():
		return userNoPassword
	default:
		return userActive
	}
}

func renderUserList(h *human, resp *pb.ListUsersResponse) error {
	us := resp.GetUsers()
	if len(us) == 0 {
		h.Note("no users")

		return h.Err()
	}

	rows := make([][]string, len(us))
	for i, u := range us {
		rows[i] = []string{u.GetName(), u.GetDisplayName(), u.GetEmail(), strings.Join(u.GetRoles(), ", "), userState(u), humanAge(u.GetCreatedAt())}
	}

	h.Table([]string{nameColumn, "display name", "email", "roles", "state", ageColumn}, rows)

	return h.Err()
}

func renderUserGet(h *human, resp *pb.GetUserResponse) error {
	u := resp.GetUser()
	h.Block(u.GetName(), kv{"Display name", u.GetDisplayName()}, kv{"E-mail", u.GetEmail()}, kv{"Roles", strings.Join(u.GetRoles(), ", ")},
		kv{"State", userState(u)}, kv{createdLabel, agoOrEmpty(u.GetCreatedAt())})
	h.Conditions("Conditions", conditionsOf(u.GetConditions()))

	return h.Err()
}

func renderUserCreated(h *human, resp *pb.CreateUserResponse) error {
	u := resp.GetUser()

	return doneLine(h, "%s %s %s%s", verbCreate, kindUser, u.GetName(), parenthesized(strings.Join(u.GetRoles(), ", ")))
}

// showGeneratedPassword prints a password the server generated, once, to errw, with the wording the
// machine formats have always had; for a person it gets a mark and is bold. password == "" prints
// nothing.
func showGeneratedPassword(errw io.Writer, name, password string) {
	if password == "" {
		return
	}

	if outputFormat != outputHuman {
		printGeneratedPassword(errw, name, password)

		return
	}

	h := newHuman(errw)
	h.Note(h.Mark(markWarning) + " " + h.Paint(sgrBold, generatedPasswordLine(name, password)))
}
