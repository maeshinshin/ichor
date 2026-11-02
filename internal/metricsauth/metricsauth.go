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

// Package metricsauth is controller-runtime's metrics filter (TokenReview, then SubjectAccessReview) with one
// change: a bearer token the cluster refuses is a 401, not a 500. controller-runtime answers 500 to every
// error of the authenticator, and the authenticator reports a token it does not accept as the error "invalid
// bearer token" (k8s.io/apiserver bearertoken). A 500 for a wrong token makes a scraper retry and hides the
// real cause; an apiserver that cannot be reached is still a 500.
package metricsauth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	authenticationv1 "k8s.io/client-go/kubernetes/typed/authentication/v1"
	authorizationv1 "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// invalidToken is the text of the authenticator's error for a token it does not accept.
const invalidToken = "invalid bearer token"

// retry is controller-runtime's own backoff (copied from k8s.io/apiserver's server options).
func retry() *wait.Backoff {
	return &wait.Backoff{Duration: 500 * time.Millisecond, Factor: 1.5, Jitter: 0.2, Steps: 5}
}

// WithAuthenticationAndAuthorization has the signature and behaviour of controller-runtime's
// filters.WithAuthenticationAndAuthorization, except for the 401 above.
func WithAuthenticationAndAuthorization(config *rest.Config, httpClient *http.Client) (metricsserver.Filter, error) {
	authn, err := authenticationv1.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, err
	}

	authz, err := authorizationv1.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, err
	}

	authenticator, _, err := authenticatorfactory.DelegatingAuthenticatorConfig{
		Anonymous:                &apiserver.AnonymousAuthConfig{Enabled: false},
		CacheTTL:                 time.Minute,
		TokenAccessReviewClient:  authn,
		TokenAccessReviewTimeout: 10 * time.Second,
		WebhookRetryBackoff:      retry(),
	}.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	authorizerFor, err := authorizerfactory.DelegatingAuthorizerConfig{
		SubjectAccessReviewClient: authz,
		AllowCacheTTL:             5 * time.Minute,
		DenyCacheTTL:              30 * time.Second,
		WebhookRetryBackoff:       retry(),
	}.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}

	return func(log logr.Logger, handler http.Handler) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			res, ok, err := authenticator.AuthenticateRequest(req)
			if err != nil && strings.Contains(err.Error(), invalidToken) {
				log.V(4).Info("Authentication failed", "error", err.Error())
				http.Error(w, "Unauthorized", http.StatusUnauthorized)

				return
			}

			if err != nil {
				log.Error(err, "Authentication failed")
				http.Error(w, "Authentication failed", http.StatusInternalServerError)

				return
			}

			if !ok {
				log.V(4).Info("Authentication failed")
				http.Error(w, "Unauthorized", http.StatusUnauthorized)

				return
			}

			attributes := authorizer.AttributesRecord{User: res.User, Verb: strings.ToLower(req.Method), Path: req.URL.Path}

			decision, reason, err := authorizerFor.Authorize(req.Context(), attributes)
			if err != nil {
				msg := fmt.Sprintf("Authorization for user %s failed", attributes.User.GetName())
				log.Error(err, msg)
				http.Error(w, msg, http.StatusInternalServerError)

				return
			}

			if decision != authorizer.DecisionAllow {
				msg := fmt.Sprintf("Authorization denied for user %s", attributes.User.GetName())
				log.V(4).Info(msg + ": " + reason)
				http.Error(w, msg, http.StatusForbidden)

				return
			}

			handler.ServeHTTP(w, req)
		}), nil
	}, nil
}
