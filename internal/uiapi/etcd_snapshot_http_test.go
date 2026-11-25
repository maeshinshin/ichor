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

package uiapi

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// generated is a snapshot of n bytes made on the fly: the test never holds it.
type generated struct{ left int64 }

func (g *generated) Read(p []byte) (int, error) {
	if g.left <= 0 {
		return 0, io.EOF
	}

	n := int(min(int64(len(p)), g.left))
	for i := range p[:n] {
		p[i] = 'S'
	}

	g.left -= int64(n)

	return n, nil
}

// drain reads r to its end, checks every byte and returns the count and the highest HeapInuse seen above the start.
func drain(r io.Reader) (n int64, peakGrowth uint64, err error) {
	runtime.GC()

	var ms runtime.MemStats

	runtime.ReadMemStats(&ms)

	base, peak := ms.HeapInuse, ms.HeapInuse
	buf := make([]byte, 64<<10)

	for {
		k, rerr := r.Read(buf)
		for _, b := range buf[:k] {
			if b != 'S' {
				return n, 0, errors.New("a byte differs")
			}
		}

		n += int64(k)

		if n%(8<<20) < int64(k) {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapInuse)
		}

		if rerr == io.EOF {
			return n, peak - base, nil
		}

		if rerr != nil {
			return n, 0, rerr
		}
	}
}

const formName = "name"

// stuckWriter is a client that stopped reading: Write blocks until the write deadline the handler set has
// passed (for ever when it set none, until stop is closed).
type stuckWriter struct {
	header   http.Header
	stop     chan struct{}
	mu       sync.Mutex
	deadline time.Time
}

func (w *stuckWriter) Header() http.Header { return w.header }
func (w *stuckWriter) WriteHeader(int)     {}
func (w *stuckWriter) SetWriteDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.deadline = t

	return nil
}

func (w *stuckWriter) Write([]byte) (int, error) {
	w.mu.Lock()
	d := w.deadline
	w.mu.Unlock()

	if d.IsZero() {
		<-w.stop

		return 0, errors.New("stopped")
	}

	select {
	case <-time.After(time.Until(d)):
		return 0, os.ErrDeadlineExceeded
	case <-w.stop:
		return 0, errors.New("stopped")
	}
}

var _ = Describe("EtcdSnapshotHandler", func() {
	var (
		cp      *ichorv1alpha1.TalosMachine
		fake    *fakeDownloadClient
		dials   atomic.Int32
		httpSrv *httptest.Server
		srv     *Server
	)

	post := func(token string) (*http.Response, error) {
		form := url.Values{"authorization": {token}, "namespace": {cp.Namespace}, formName: {cp.Name}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpSrv.URL+EtcdSnapshotPath, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		return http.DefaultClient.Do(req)
	}

	BeforeEach(func() {
		rules := map[string][]rbacv1.PolicyRule{
			diagAdminUser: {
				{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{resourceTalosMachines, resourceTalosClusters}, Verbs: []string{cniVerbGet, cniVerbList}},
				{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{"talosmachines/etcd-snapshot"}, Verbs: []string{DownloadVerb}},
				{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{credentialRequestsResource}, Verbs: []string{cniVerbCreate}},
			},
			diagNobodyUser: {{APIGroups: []string{ichorv1alpha1.GroupVersion.Group}, Resources: []string{resourceTalosMachines, resourceTalosClusters}, Verbs: []string{cniVerbGet, cniVerbList}}},
		}

		for user, r := range rules {
			name := "snap-http-" + user
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: r}))).To(Succeed())
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
				Name:     name,
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: cniClusterRole, Name: name},
				Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
			}))).To(Succeed())
		}

		cluster := newTestClusterWithBundle()
		cp = newTestMachine(cluster.Name, strongTestIdentity("uuid-snap-http"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
		fake = &fakeDownloadClient{}
		dials.Store(0)

		_, s, closeRPC := newDiagnosticsServer(fake, k8sClient, &dials)
		srv = s
		DeferCleanup(closeRPC)

		authenticate := func(_ context.Context, header string) (Identity, error) {
			switch header {
			case "Bearer admin":
				return Identity{User: diagAdminUser}, nil
			case "Bearer short":
				return Identity{User: diagAdminUser, Expires: time.Now().Add(100 * time.Millisecond)}, nil
			case "Bearer nobody":
				return Identity{User: diagNobodyUser}, nil
			}

			return Identity{}, errors.New("missing bearer token")
		}

		httpSrv = httptest.NewServer(s.EtcdSnapshotHandler(authenticate, log.New(GinkgoWriter, "", 0)))
		DeferCleanup(httpSrv.Close)
	})

	It("sends 128 MiB as an attachment with every byte and a bounded heap", func() {
		const size = 128 << 20

		fake.stream = &generated{left: size}

		resp, err := post("admin")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Disposition")).To(HavePrefix("attachment; filename=etcd-" + cp.Name))
		Expect(resp.ContentLength).To(BeNumerically("==", -1), "streamed, not buffered to learn a length")

		n, growth, err := drain(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(size))
		Expect(growth).To(BeNumerically("<", 32<<20), "the heap grew by the size of the snapshot, not by a copy buffer")
		Expect(fake.closed.Load()).To(BeTrue())
		Expect(downloadEvents(cp)).NotTo(BeEmpty())
	})

	It("does not refuse a snapshot over 1 GiB", func() {
		const size = 1<<30 + 1<<20

		fake.stream = &generated{left: size}

		resp, err := post("admin")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		n, _, err := drain(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(size))
	})

	It("refuses a caller without the download verb before dialing Talos", func() {
		fake.stream = &generated{left: 1 << 20}

		resp, err := post("nobody")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(dials.Load()).To(BeZero())
	})

	It("refuses a request with no valid token", func() {
		resp, err := post("")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(dials.Load()).To(BeZero())
	})

	It("does not cut a slow stream that keeps moving past the idle period", func() {
		old := etcdSnapshotIdle
		etcdSnapshotIdle = 150 * time.Millisecond

		DeferCleanup(func() { etcdSnapshotIdle = old })

		// 30 chunks, 20 ms apart: 600 ms in all, four times the idle period.
		fake.streamFor = func(c context.Context) io.Reader {
			return &paced{left: 30 << 10, chunk: 1 << 10, gap: 20 * time.Millisecond, ctx: c}
		}

		resp, err := post("admin")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		n, _, err := drain(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(30 << 10))
	})

	It("frees its download slot when the client stops reading", func() {
		old := etcdSnapshotIdle
		etcdSnapshotIdle = 100 * time.Millisecond

		DeferCleanup(func() { etcdSnapshotIdle = old })

		fake.stream = &generated{left: 1 << 20}

		authenticate := func(context.Context, string) (Identity, error) { return Identity{User: diagAdminUser}, nil }
		h := srv.EtcdSnapshotHandler(authenticate, log.New(GinkgoWriter, "", 0))

		w := &stuckWriter{header: http.Header{}, stop: make(chan struct{})}
		DeferCleanup(func() { close(w.stop) })

		form := url.Values{"authorization": {"admin"}, "namespace": {cp.Namespace}, formName: {cp.Name}}
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, EtcdSnapshotPath, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		returned := make(chan struct{})

		go func() {
			defer close(returned)
			defer func() { _ = recover() }() // the handler aborts the response with http.ErrAbortHandler

			h.ServeHTTP(w, r)
		}()

		Eventually(returned, 5*time.Second).Should(BeClosed(), "the handler stayed blocked on a client that stopped reading")

		for range maxConcurrentDownloads {
			release, err := srv.acquireDownloadSlot()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(release)
		}
	})

	It("cuts a stalled stream after the idle period", func() {
		old := etcdSnapshotIdle
		etcdSnapshotIdle = 100 * time.Millisecond

		DeferCleanup(func() { etcdSnapshotIdle = old })

		fake.streamFor = func(c context.Context) io.Reader {
			return &paced{left: 1 << 10, chunk: 1 << 10, ctx: c, stallAfter: true}
		}

		resp, err := post("admin")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		start := time.Now()
		_, err = io.Copy(io.Discard, resp.Body)
		Expect(err).To(HaveOccurred(), "a stalled snapshot must not look like a whole file")
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	It("does not cut a transfer that is still moving when the token expires", func() {
		fake.streamFor = func(c context.Context) io.Reader {
			return &paced{left: 30 << 10, chunk: 1 << 10, gap: 20 * time.Millisecond, ctx: c}
		}

		resp, err := post("short")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		n, _, err := drain(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeEquivalentTo(30<<10), "the stream lasts 600 ms, the token 100 ms")
	})

	It("never lets a caller who may not download take a download slot", func() {
		srv.initDiag()

		for range maxConcurrentDownloads {
			srv.diag.slots <- struct{}{}
		}

		DeferCleanup(func() {
			for range maxConcurrentDownloads {
				<-srv.diag.slots
			}
		})

		resp, err := post("nobody")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden), "refused for permission, not for a full house")
	})

	It("aborts the response when the stream breaks, so no short file looks whole", func() {
		fake.stream = io.MultiReader(&generated{left: 1 << 20}, &brokenStream{})

		resp, err := post("admin")
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = resp.Body.Close() }()

		_, err = io.Copy(io.Discard, resp.Body)
		Expect(err).To(HaveOccurred())
	})
})

// paced yields chunks of 'S' every gap until total bytes are out, like a slow link that keeps moving.
type paced struct {
	left       int
	chunk      int
	gap        time.Duration
	ctx        context.Context
	stallAfter bool // after the data, block until the context ends instead of reaching EOF
}

func (p *paced) Read(b []byte) (int, error) {
	if p.left <= 0 {
		if !p.stallAfter {
			return 0, io.EOF
		}

		<-p.ctx.Done()

		return 0, p.ctx.Err()
	}

	select {
	case <-time.After(p.gap):
	case <-p.ctx.Done():
		return 0, p.ctx.Err()
	}

	n := min(len(b), p.chunk, p.left)
	for i := range b[:n] {
		b[i] = 'S'
	}

	p.left -= n

	return n, nil
}

type brokenStream struct{}

func (brokenStream) Read([]byte) (int, error) { return 0, errors.New("talos went away") }
