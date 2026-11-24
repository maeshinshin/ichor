package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

// installerCheckPoll and installerCheckWait are how often, and for how long, the CLI waits for the check Job. The Job's
// own deadline is shorter; the wait also covers a pod that cannot be scheduled or pulled.
var (
	installerCheckPoll = 2 * time.Second
	installerCheckWait = 5 * time.Minute
)

// runInstallerCheck runs `installer check` of the target version's installer image as a short Job, before the upgrade
// writes anything, and returns its refusal verbatim. The image is pinned and verified as the upgrade pins it
// (digest and skipSignature are --installer-digest and --skip-signature-check). It writes the Job and its pod, and
// deletes the Job again (background propagation).
func runInstallerCheck(ctx context.Context, k client.Client, images controller.InstallerImages, errw io.Writer, target, digest string, skipSignature bool) error {
	var inst ichorv1alpha1.IchorInstallation
	if err := k.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		return fmt.Errorf("upgrade: reading IchorInstallation: %w", err)
	}

	// The upgrade is not started yet, so spec still names the running version: ask for the target's image, with the
	// pin the upgrade would write (a flag not given clears an earlier pin or skip, as the upgrade does).
	want := inst.DeepCopy()
	want.Spec.Version, want.Spec.InstallerDigest, want.Spec.InstallerSignature = "v"+strings.TrimPrefix(target, "v"), digest, nil

	if skipSignature {
		want.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip}
	}

	// Ctrl-C ends the wait instead of the process, so the deferred delete below runs and no Job is left behind.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pinned, err := controller.PinInstallerImage(ctx, images, want)
	if err != nil {
		return fmt.Errorf("upgrade: checking the upgrade before it writes: %w", err)
	}

	name := "ichor-installer-check-" + rand.String(5)
	job := controller.NewInstallerCheckJob(ichorControllerNamespace, name, want, pinned, "", false)

	if err := k.Create(ctx, job); err != nil {
		return fmt.Errorf("upgrade: starting the check Job: %w", err)
	}

	defer func() {
		// A fresh context: the run may have been cancelled, and the Job must still go.
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		if err := k.Delete(dctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
			_, _ = fmt.Fprintf(errw, "upgrade: could not delete the check Job %s/%s: %v\n", job.Namespace, name, err)
		}
	}()

	deadline := time.Now().Add(installerCheckWait)

	for {
		if err := k.Get(ctx, client.ObjectKeyFromObject(job), job); err != nil {
			return fmt.Errorf("upgrade: reading the check Job: %w", err)
		}

		if done, failed := checkJobOutcome(job); done {
			return nil
		} else if failed != "" {
			return checkJobRefusal(ctx, k, job, failed)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("upgrade: the check Job %s did not finish in %s (see its pod in %s); nothing was written", name, installerCheckWait, ichorControllerNamespace)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(installerCheckPoll):
		}
	}
}

// checkJobOutcome is done for a Complete Job, and the failure's reason for a Failed one.
func checkJobOutcome(job *batchv1.Job) (done bool, failed string) {
	for _, c := range job.Status.Conditions {
		switch {
		case c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue:
			return true, ""
		case c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue:
			return false, c.Reason + ": " + c.Message
		}
	}

	return false, ""
}

// checkJobRefusal is the installer's own message from the failed pod, verbatim. A Job that failed without one never
// reached the check: the image does not know `installer check` (it predates it), or it could not start.
func checkJobRefusal(ctx context.Context, k client.Client, job *batchv1.Job, failed string) error {
	msg, err := controller.JobTerminationMessage(ctx, k, job.Namespace, job.Name)
	if err != nil {
		return fmt.Errorf("upgrade: the check Job %s failed (%s) and its pod could not be read: %w", job.Name, failed, err)
	}

	if msg == "" {
		return errors.New("upgrade: the check Job " + job.Name + " failed (" + failed + ") without a message from the installer. " +
			"If the target image predates `installer check`, it does not know that command and the check cannot run; " +
			"otherwise read the pod log of the Job in " + job.Namespace + ". Nothing was written")
	}

	return fmt.Errorf("upgrade: %s\nnothing was written", msg)
}
