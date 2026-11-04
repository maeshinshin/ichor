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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// userCmd groups the IchorUser subcommands. They call
// UserService on ichor-ui at --issuer (the same URL the login uses) with an
// ID token for audience ichor-ui, so the caller's Kubernetes RBAC decides
// what they may do. A password is never a flag: it comes from a prompt with
// no echo, from stdin (--password-stdin), or is generated.
var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage Ichor's own users (list, create, update, delete, set-password, revoke-sessions)",
	Long: `Manage the users of Ichor's own login. These commands talk to
ichor-ui at --issuer over HTTPS with your login (run ` + "`ichor auth login --issuer URL --audience ichor-ui`" + `
or let the first command open the browser). Use --ca-file for a private CA.

To create the very first admin on an existing install, use
  ichor user create NAME --role admin --generate --bootstrap
which needs no login: it writes the user with your kubeconfig's own rights.`,
}

const useUserDelete = "delete NAME"

var userOpts struct {
	displayName   string
	email         string
	roles         []string
	disabled      bool
	passwordStdin bool
	generate      bool
	bootstrap     bool
}

func init() {
	pf := userCmd.PersistentFlags()
	pf.StringVar(&authOpts.issuer, "issuer", os.Getenv("ICHOR_ISSUER"), "external https URL of ichor-ui, which is Ichor's OIDC issuer (env ICHOR_ISSUER)")
	addCAFileFlag(pf)

	userCreateCmd.Flags().StringVar(&userOpts.displayName, "display-name", "", "display name")
	userCreateCmd.Flags().StringVar(&userOpts.email, "email", "", "e-mail address (informational)")
	userCreateCmd.Flags().StringSliceVar(&userOpts.roles, "role", nil, "role (viewer, operator, admin or your own); repeatable")
	userCreateCmd.Flags().BoolVar(&userOpts.bootstrap, "bootstrap", false,
		"write the user and its password hash with your kubeconfig's own rights instead of through ichor-ui (for the first admin of an existing install)")
	addPasswordFlags(userCreateCmd)

	userUpdateCmd.Flags().StringVar(&userOpts.displayName, "display-name", "", "new display name")
	userUpdateCmd.Flags().StringVar(&userOpts.email, "email", "", "new e-mail address")
	userUpdateCmd.Flags().StringSliceVar(&userOpts.roles, "role", nil, "replace the roles with these; repeatable")
	userUpdateCmd.Flags().BoolVar(&userOpts.disabled, "disabled", false, "disable (true) or enable (false) the user")

	addPasswordFlags(userSetPasswordCmd)

	userCmd.AddCommand(userListCmd, userGetCmd, userCreateCmd, userUpdateCmd, userDeleteCmd, userSetPasswordCmd, userRevokeCmd)
	rootCmd.AddCommand(userCmd)
}

func addPasswordFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&userOpts.passwordStdin, "password-stdin", false, "read the password from the first line of stdin (no prompt)")
	cmd.Flags().BoolVar(&userOpts.generate, "generate", false, "generate a password; it is printed once to stderr and stored nowhere")
}

// withUsers dials ichor-ui and runs fn with the UserService client.
func withUsers(cmd *cobra.Command, fn func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error) error {
	if err := validateOutputFormat(outputFormat); err != nil {
		return err
	}

	sess, err := dialUI(cmd, true)
	if err != nil {
		return err
	}

	return fn(cmd.Context(), sess.Users)
}

var userListCmd = &cobra.Command{
	Use:   useList,
	Short: "List users",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			resp, err := u.ListUsers(ctx, connect.NewRequest(&pb.ListUsersRequest{}))
			if err != nil {
				return err
			}

			return printAPI(cmd.OutOrStdout(), resp.Msg, renderUserList)
		})
	},
}

var userGetCmd = &cobra.Command{
	Use:   useGetName,
	Short: "Show one user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			resp, err := u.GetUser(ctx, connect.NewRequest(&pb.GetUserRequest{Name: args[0]}))
			if err != nil {
				return err
			}

			return printAPI(cmd.OutOrStdout(), resp.Msg, renderUserGet)
		})
	},
}

var userCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a user and set its first password (it must be changed at the first sign-in)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if userOpts.bootstrap {
			return runUserBootstrap(cmd, args[0])
		}

		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			return runUserCreate(ctx, u, cmd, args[0])
		})
	},
}

func runUserCreate(ctx context.Context, u ichorv1alpha1connect.UserServiceClient, cmd *cobra.Command, name string) error {
	// Ask for the password before creating anything, so a typo or a closed stdin leaves nothing behind.
	password, err := resolvePassword(cmd, userOpts.passwordStdin, userOpts.generate)
	if err != nil {
		return err
	}

	resp, err := u.CreateUser(ctx, connect.NewRequest(&pb.CreateUserRequest{
		Name: name, DisplayName: userOpts.displayName, Email: userOpts.email, Roles: userOpts.roles,
	}))
	if err != nil {
		return err
	}

	generated, err := setPassword(ctx, u, name, password)
	if err != nil {
		return fmt.Errorf("user %s was created but has no password yet: %w (run `ichor user set-password %s`)", name, err, name)
	}

	// The generated password comes last, under the result, and is printed even when that cannot be written.
	err = printAPI(cmd.OutOrStdout(), resp.Msg, renderUserCreated)

	showGeneratedPassword(cmd.ErrOrStderr(), name, generated)

	return err
}

var userUpdateCmd = &cobra.Command{
	Use:   "update NAME",
	Short: "Change a user's display name, e-mail, roles or disabled flag (only the flags you pass)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			return runUserUpdate(ctx, u, cmd, args[0])
		})
	},
}

func runUserUpdate(ctx context.Context, u ichorv1alpha1connect.UserServiceClient, cmd *cobra.Command, name string) error {
	cur, err := u.GetUser(ctx, connect.NewRequest(&pb.GetUserRequest{Name: name}))
	if err != nil {
		return err
	}

	req := &pb.UpdateUserRequest{
		Name: name, DisplayName: cur.Msg.GetUser().GetDisplayName(), Email: cur.Msg.GetUser().GetEmail(),
		Roles: cur.Msg.GetUser().GetRoles(), Disabled: cur.Msg.GetUser().GetDisabled(),
	}

	fl := cmd.Flags()
	changed := false

	if fl.Changed("display-name") {
		req.DisplayName, changed = userOpts.displayName, true
	}

	if fl.Changed("email") {
		req.Email, changed = userOpts.email, true
	}

	if fl.Changed("role") {
		req.Roles, changed = userOpts.roles, true
	}

	if fl.Changed("disabled") {
		req.Disabled, changed = userOpts.disabled, true
	}

	if !changed {
		return errors.New("nothing to change: pass --display-name, --email, --role or --disabled")
	}

	resp, err := u.UpdateUser(ctx, connect.NewRequest(req))
	if err != nil {
		return err
	}

	return printAPI(cmd.OutOrStdout(), resp.Msg, func(h *human, _ *pb.UpdateUserResponse) error {
		return doneLine(h, "%s %s %s", verbUpdate, kindUser, name)
	})
}

var userDeleteCmd = &cobra.Command{
	Use:   useUserDelete,
	Short: "Delete a user (the last enabled admin cannot be deleted)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			return runUserDelete(ctx, u, cmd, args[0])
		})
	},
}

func runUserDelete(ctx context.Context, u ichorv1alpha1connect.UserServiceClient, cmd *cobra.Command, name string) error {
	if !assumeYes {
		if err := confirmOrAbort(cmd.InOrStdin(), cmd.ErrOrStderr(), "user delete", fmt.Sprintf("Delete user %s? Their sessions stop working within minutes. [y/N] ", name), "aborted"); err != nil {
			return err
		}
	}

	resp, err := u.DeleteUser(ctx, connect.NewRequest(&pb.DeleteUserRequest{Name: name}))
	if err != nil {
		return err
	}

	return printAPI(cmd.OutOrStdout(), resp.Msg, func(h *human, _ *pb.DeleteUserResponse) error {
		return doneLine(h, "%s %s %s", verbDelete, kindUser, name)
	})
}

var userSetPasswordCmd = &cobra.Command{
	Use:   "set-password NAME",
	Short: "Set a user's password as an administrator (the user must change it at the next sign-in)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			return runUserSetPassword(ctx, u, cmd, args[0])
		})
	},
}

func runUserSetPassword(ctx context.Context, u ichorv1alpha1connect.UserServiceClient, cmd *cobra.Command, name string) error {
	password, err := resolvePassword(cmd, userOpts.passwordStdin, userOpts.generate)
	if err != nil {
		return err
	}

	generated, err := setPassword(ctx, u, name, password)
	if err != nil {
		return err
	}

	// The generated password comes last, under the result, and is printed even when that cannot be written.
	if outputFormat == outputHuman {
		err = doneLine(newHuman(cmd.OutOrStdout()), "set the password of %s (they must change it at the next sign-in)", name)
	}

	showGeneratedPassword(cmd.ErrOrStderr(), name, generated)

	return err
}

var userRevokeCmd = &cobra.Command{
	Use:   "revoke-sessions NAME",
	Short: "Sign a user out everywhere (kubectl tokens last at most the token lifetime)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withUsers(cmd, func(ctx context.Context, u ichorv1alpha1connect.UserServiceClient) error {
			return runUserRevoke(ctx, u, cmd, args[0])
		})
	},
}

func runUserRevoke(ctx context.Context, u ichorv1alpha1connect.UserServiceClient, cmd *cobra.Command, name string) error {
	resp, err := u.RevokeSessions(ctx, connect.NewRequest(&pb.RevokeSessionsRequest{Name: name}))
	if err != nil {
		return err
	}

	return printAPI(cmd.OutOrStdout(), resp.Msg, func(h *human, _ *pb.RevokeSessionsResponse) error {
		return doneLine(h, "revoked the sessions of %s", name)
	})
}

// setPassword calls SetPassword and returns the password the server generated, "" when the caller
// gave one. password == "" means "generate". The caller prints a generated password, once.
func setPassword(ctx context.Context, u ichorv1alpha1connect.UserServiceClient, name, password string) (string, error) {
	resp, err := u.SetPassword(ctx, connect.NewRequest(&pb.SetPasswordRequest{Name: name, Password: password}))
	if err != nil {
		return "", err
	}

	return resp.Msg.GetGeneratedPassword(), nil
}

func generatedPasswordLine(name, password string) string {
	return fmt.Sprintf("Password for %s (shown once, stored nowhere; they must change it at the first sign-in): %s", name, password)
}

func printGeneratedPassword(errw io.Writer, name, password string) {
	_, _ = fmt.Fprintln(errw, generatedPasswordLine(name, password))
}

// readPasswordFn reads a line without echo from the terminal; a test replaces it.
var readPasswordFn = func(prompt string, errw io.Writer) (string, error) {
	fd := int(os.Stdin.Fd()) //nolint:gosec // a file descriptor fits an int

	if !term.IsTerminal(fd) {
		return "", errors.New("no terminal to prompt on: pass --password-stdin or --generate")
	}

	_, _ = fmt.Fprint(errw, prompt)

	b, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(errw)

	return string(b), err
}

// resolvePassword returns the password to set, or "" to have one generated. It
// is never taken from a flag value.
func resolvePassword(cmd *cobra.Command, fromStdin, generate bool) (string, error) {
	switch {
	case fromStdin && generate:
		return "", errors.New("--password-stdin and --generate exclude each other")
	case generate:
		return "", nil
	case fromStdin:
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading the password from stdin: %w", err)
		}

		if line = strings.TrimRight(line, "\r\n"); line == "" {
			return "", errors.New("the first line of stdin is empty: no password")
		}

		return line, nil
	}

	first, err := readPasswordFn("Password: ", cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}

	second, err := readPasswordFn("Repeat password: ", cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}

	if first != second {
		return "", errors.New("the two passwords differ")
	}

	if first == "" {
		return "", errors.New("empty password")
	}

	return first, nil
}
