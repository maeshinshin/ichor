// Package rbacwrites holds the check that every write the ichor-ui ServiceAccount makes is granted by the
// role generated from internal/uiapi/rbac_markers.go, config/rbac/ichor-ui/role.yaml.
//
// It reads internal/uiapi with type information (go/packages), so it sees the object each write targets
// and the client the write is made with. The client is told apart by how the call is written:
//
//   - s.Client.X on a *uiapi.Server: the embedded client.Client, which is the ServiceAccount.
//   - s.Create, s.Update, s.Delete, s.Patch and s.DeleteAllOf on a *uiapi.Server: the methods in auth.go, which use the signed-in
//     user's impersonating client when the context carries an identity. AuthInterceptor puts one on every
//     RPC, so these are the user's writes and the role does not have to grant them.
//
// A write through any other client (a parameter, a field) is a site in the unresolved table below, with the
// origin of that client written beside it. A write the table does not name fails the check: it is never
// skipped.
//
// The role is read from config/rbac/ichor-ui/role.yaml, the generated copy. The chart's hand-kept mirror,
// dist/chart/templates/rbac/ichor-ui-role.yaml, is compared with that file by checkUIBackendRBACMatchesConfig
// in dist/chart/chart_test.go, so this check does not read the mirror a second time.
//
// Limits: a rule with resourceNames counts as granting its verbs on the resource, because the write's
// object name is not a static value here. The namespace of a write is not checked either; a Role and a
// ClusterRole are read as one set of grants.
package rbacwrites

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	repoRoot    = "../../.."
	uiapiPath   = "github.com/maeshinshin/ichor/internal/uiapi"
	clientPath  = "sigs.k8s.io/controller-runtime/pkg/client"
	roleFile    = "config/rbac/ichor-ui/role.yaml"
	authFile    = "auth.go"
	markersFile = "internal/uiapi/rbac_markers.go"
)

// writeVerbs maps each controller-runtime write method to the RBAC verb it needs.
var writeVerbs = map[string]string{
	"Create": "create", "Update": "update", "Patch": "patch", "Delete": "delete", "DeleteAllOf": "deletecollection",
}

// origin is which client a write goes through.
type origin int

const (
	// serviceAccount: the write is made with the ServiceAccount's own client, so the role must grant it.
	serviceAccount origin = iota
	// signedInUser: the write is made with the signed-in user's client, so the role need not grant it.
	signedInUser
	// forwarder: the write is a call on a client that another function already chose, such as the
	// wrappers in auth.go that forward to cl(ctx). Its caller is the write that matters.
	forwarder
)

// site names a write whose client is a variable, a parameter or a field rather than s or s.Client.
type site struct{ file, fn, recv string }

// unresolved gives the origin of each such write. Every entry is one line of evidence; a write the check
// cannot place and this table does not name fails the check.
var unresolved = map[site]origin{
	// The shadowing methods forward to cl(ctx), the client the caller's context picks: the signed-in
	// user's whenever the RPC carries an identity, which AuthInterceptor guarantees. The object is an
	// interface here, so these are not kinds of their own; the write is checked where the caller makes it.
	{authFile, "*Server.Create", "c"}:      forwarder,
	{authFile, "*Server.Update", "c"}:      forwarder,
	{authFile, "*Server.Delete", "c"}:      forwarder,
	{authFile, "*Server.Patch", "c"}:       forwarder,
	{authFile, "*Server.DeleteAllOf", "c"}: forwarder,
	// Status() and SubResource() return subResource, which picks the client per call the same way.
	{authFile, "subResource.Create", "c"}: forwarder,
	{authFile, "subResource.Update", "c"}: forwarder,
	{authFile, "subResource.Patch", "c"}:  forwarder,

	// credentialAnswer is called with s.Client (nodeReader, the public facts: credissuer.go:182,207) and
	// with s.cl(ctx) (issueAsCaller). It is checked as the ServiceAccount, the stricter reading.
	{"credissuer.go", "*Server.credentialAnswer", "c"}: serviceAccount,

	// askAll's client is c, err := s.cl(ctx) (diagnostics_service.go:269): the signed-in user's.
	{"diagnostics_service.go", "*Server.askAll", "c"}: signedInUser,

	// waitForImpersonationRule's client is client.New(cfg), the ServiceAccount's own (NewImpersonator's cfg).
	{authFile, "waitForImpersonationRule", "self"}: serviceAccount,

	// cmd/ichor-ui/main.go:214 sets IssuerBackend.Writer to c = client.New(restCfg), the ServiceAccount's
	// own client, and nothing else in non-test code sets it.
	{"issuer_backend.go", "*IssuerBackend.rehash", "b.Writer"}:          serviceAccount,
	{"refresh_store.go", "*IssuerBackend.updateCredential", "b.Writer"}: serviceAccount,
}

// exception is one write the role does not grant that the check accepts on purpose. It matches one
// (file, function, verb, resource); a matching exception that is never hit fails the check, so an exception
// cannot outlive the write it describes.
type exception struct {
	file, fn, verb, resource string
	reason                   string
}

var exceptions = []exception{
	{
		file: "reclaim_service.go", fn: "*Server.reclaimUser", verb: "create", resource: "selfsubjectreviews",
		reason: "reclaimUser asks Kubernetes who the backend is only when the request has no identity or the server has " +
			"no Impersonate. ichor-ui never reaches that: AuthInterceptor puts an identity on every RPC and cmd/ichor-ui " +
			"sets Impersonate unconditionally (cmd/ichor-ui/main.go). It is live only on internal/cli/api.go's " +
			"CallerCredentials path, where the client is the CLI user's own kubeconfig, so the permission is Kubernetes' " +
			"built-in system:basic-user (bound to system:authenticated, kubernetes.io RBAC default roles), not the ichor-ui role. " +
			"Do not grant selfsubjectreviews to the role to silence this; delete the entry if the fallback goes.",
	},
}

// grant is one (group, resource, verb) the role gives, read from its rules.
type grant struct{ group, resource, verb string }

func TestUIWritesAreGrantedByTheRole(t *testing.T) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: root,
	}, uiapiPath)
	if err != nil {
		t.Fatal(err)
	}

	if len(pkgs) != 1 {
		t.Fatalf("loaded %d packages for %s, want 1", len(pkgs), uiapiPath)
	}

	for _, e := range pkgs[0].Errors {
		t.Fatalf("loading %s: %v", uiapiPath, e)
	}

	known, err := knownKinds()
	if err != nil {
		t.Fatal(err)
	}

	granted := roleGrants(t, filepath.Join(root, roleFile))

	w := &walker{root: root, pkg: pkgs[0], known: known, granted: granted, exceptionsUsed: make([]bool, len(exceptions))}
	for _, f := range pkgs[0].Syntax {
		w.file(f)
	}

	// The check must have looked at real writes on both sides, or a pass means nothing.
	if w.serviceAccountWrites == 0 || w.userWrites == 0 {
		t.Fatalf("found %d ServiceAccount writes and %d user writes in %s; want both above zero",
			w.serviceAccountWrites, w.userWrites, uiapiPath)
	}

	for i, e := range exceptions {
		if !w.exceptionsUsed[i] {
			t.Errorf("exception for %s in %s (%s %s) matches no write; delete it", e.file, e.fn, e.verb, e.resource)
		}
	}

	for _, msg := range w.failures {
		t.Error(msg)
	}
}

// walker visits one package's files and records the failures it finds.
type walker struct {
	root    string
	pkg     *packages.Package
	known   map[string]schema.GroupVersionKind
	granted []grant

	serviceAccountWrites, userWrites int
	exceptionsUsed                   []bool
	failures                         []string
}

func (w *walker) file(f *ast.File) {
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		fn := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) == 1 {
			fn = types.ExprString(fd.Recv.List[0].Type) + "." + fn
		}

		// called holds the selectors that are the Fun of a call. ast.Inspect visits a call before its
		// Fun, so a write selector that is not in it is a method value, whose target is not visible.
		called := map[*ast.SelectorExpr]bool{}

		ast.Inspect(fd, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
					called[sel] = true
					if w.writeMethod(sel) {
						w.call(sel, n, fn)
					}
				}
			case *ast.SelectorExpr:
				if w.writeMethod(n) && !called[n] {
					w.fail(n.Pos(), fn, fmt.Sprintf("%s is used as a value, not called, so its object cannot be read", n.Sel.Name))
				}
			}

			return true
		})
	}
}

// writeMethod reports whether sel names a controller-runtime write method, or the Server method that
// shadows one.
func (w *walker) writeMethod(sel *ast.SelectorExpr) bool {
	fn, ok := w.pkg.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return false
	}

	if _, isWrite := writeVerbs[fn.Name()]; !isWrite {
		return false
	}

	switch fn.Pkg().Path() {
	case clientPath:
		return true
	case uiapiPath:
		// The shadowing methods are the Server's own; any other method of this package is not a write.
		return isServerType(w.pkg.TypesInfo.TypeOf(sel.X))
	}

	return false
}

func (w *walker) call(sel *ast.SelectorExpr, call *ast.CallExpr, fn string) {
	pos := w.pkg.Fset.Position(call.Pos())
	verb := writeVerbs[sel.Sel.Name]

	who, found := w.origin(sel, fn, pos.Filename)
	if !found {
		w.fail(call.Pos(), fn, fmt.Sprintf("the %s is made through %s, a client this check does not know; add it to the unresolved table with its origin",
			verb, types.ExprString(sel.X)))
		return
	}

	if who == forwarder {
		return
	}

	if len(call.Args) < 2 {
		w.fail(call.Pos(), fn, fmt.Sprintf("%s has no object argument", sel.Sel.Name))
		return
	}

	if _, ok := sel.X.(*ast.CallExpr); ok {
		w.fail(call.Pos(), fn, "the write goes through a call (a SubResource or Status writer?), which this check does not model")
		return
	}

	kind, ok := w.kindOf(w.pkg.TypesInfo.TypeOf(call.Args[1]))
	if !ok {
		w.fail(call.Pos(), fn, fmt.Sprintf("the object argument has type %s, which is not a concrete Kubernetes kind",
			w.pkg.TypesInfo.TypeOf(call.Args[1])))
		return
	}

	// The first result is the plural resource; the second is the singular.
	plural, _ := meta.UnsafeGuessKindToResource(kind)
	resource := plural.Resource

	switch who {
	case signedInUser:
		w.userWrites++
	case serviceAccount:
		w.serviceAccountWrites++

		if w.allowed(kind.Group, resource, verb) {
			return
		}

		if w.excepted(fn, pos.Filename, verb, resource) {
			return
		}

		w.fail(call.Pos(), fn, fmt.Sprintf("the ServiceAccount's own client makes a %s on %s (group %q), and %s grants no such verb; "+
			"grant it with a +kubebuilder:rbac marker in %s and regenerate, or move the write to the signed-in user's client",
			verb, resource, kind.Group, roleFile, markersFile))
	}
}

// excepted reports whether an exception covers the write, and marks it as used.
func (w *walker) excepted(fn, file, verb, resource string) bool {
	base := filepath.Base(file)
	for i, e := range exceptions {
		if e.file == base && e.fn == fn && e.verb == verb && e.resource == resource {
			w.exceptionsUsed[i] = true

			return true
		}
	}

	return false
}

// origin says which client sel's receiver is. It reads the call's shape first (s.Client.X, s.Patch) and
// falls back to the unresolved table.
func (w *walker) origin(sel *ast.SelectorExpr, fn, file string) (origin, bool) {
	info := w.pkg.TypesInfo

	if isServerType(info.TypeOf(sel.X)) {
		if f, ok := info.Uses[sel.Sel].(*types.Func); ok && f.Pkg().Path() == uiapiPath {
			return signedInUser, true
		}

		return serviceAccount, true // a method promoted from the embedded client.Client
	}

	if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Client" && isServerType(info.TypeOf(inner.X)) {
		return serviceAccount, true
	}

	o, ok := unresolved[site{file: filepath.Base(file), fn: fn, recv: types.ExprString(sel.X)}]

	return o, ok
}

// allowed reports whether any rule of the role grants verb on resource in group.
func (w *walker) allowed(group, resource, verb string) bool {
	for _, g := range w.granted {
		if (g.group == group || g.group == "*") && (g.resource == resource || g.resource == "*") &&
			(g.verb == verb || g.verb == "*") {
			return true
		}
	}

	return false
}

func (w *walker) fail(pos token.Pos, fn, why string) {
	at := w.pkg.Fset.Position(pos)
	rel, err := filepath.Rel(w.root, at.Filename)
	if err != nil {
		rel = at.Filename
	}

	w.failures = append(w.failures, fmt.Sprintf("%s:%d (in %s): %s", filepath.ToSlash(rel), at.Line, fn, why))
}

// kindOf maps a Go type to the GroupVersionKind the scheme registers it under.
func (w *walker) kindOf(t types.Type) (schema.GroupVersionKind, bool) {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}

	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return schema.GroupVersionKind{}, false
	}

	gvk, ok := w.known[n.Obj().Pkg().Path()+"."+n.Obj().Name()]

	return gvk, ok
}

func isServerType(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}

	n, ok := t.(*types.Named)

	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == uiapiPath && n.Obj().Name() == "Server"
}

// knownKinds maps "package.Name" of every type the controller's scheme registers to its kind.
func knownKinds() (map[string]schema.GroupVersionKind, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}

	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		return nil, err
	}

	out := map[string]schema.GroupVersionKind{}
	for gvk, rt := range s.AllKnownTypes() {
		key := rt.PkgPath() + "." + rt.Name()
		if prev, ok := out[key]; ok && prev != gvk {
			// A type registered under two kinds has no single resource here; it is dropped and a write to it
			// fails as an unknown kind rather than being matched to the wrong one.
			out[key] = schema.GroupVersionKind{}

			continue
		}

		out[key] = gvk
	}

	for key, gvk := range out {
		if gvk.Kind == "" {
			delete(out, key)
		}
	}

	return out, nil
}

// roleGrants reads every ClusterRole and Role in the file and returns the grants they give, one per
// (group, resource, verb). It fails if the file yields none, so an unreadable role cannot pass.
func roleGrants(t *testing.T, path string) []grant {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var out []grant

	for doc := range strings.SplitSeq(string(data), "\n---\n") {
		var head struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(doc), &head); err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		var rules []rbacv1.PolicyRule

		switch head.Kind {
		case "ClusterRole":
			var cr rbacv1.ClusterRole
			if err := yaml.Unmarshal([]byte(doc), &cr); err != nil {
				t.Fatalf("%s: %v", path, err)
			}

			rules = cr.Rules
		case "Role":
			var r rbacv1.Role
			if err := yaml.Unmarshal([]byte(doc), &r); err != nil {
				t.Fatalf("%s: %v", path, err)
			}

			rules = r.Rules
		default:
			continue
		}

		for _, r := range rules {
			for _, g := range r.APIGroups {
				for _, res := range r.Resources {
					for _, v := range r.Verbs {
						out = append(out, grant{group: g, resource: res, verb: v})
					}
				}
			}
		}
	}

	if len(out) == 0 {
		t.Fatalf("%s: no ClusterRole or Role rules parsed", path)
	}

	return out
}
