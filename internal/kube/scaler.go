// Package kube scales a Deployment of the app's own namespace through the
// in-cluster Kubernetes API (its scale subresource), so an on-demand pod
// runs only while it is needed. It needs a ServiceAccount token and a Role
// with get/patch on deployments/scale and get on the deployment.
package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// In-cluster credentials, as Kubernetes mounts them.
const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// Scaler scales one Deployment.
type Scaler struct {
	base       string // https://host:port
	namespace  string
	deployment string
	tokenFile  string
	http       *http.Client
}

// InCluster returns a scaler for a Deployment of the pod's namespace.
func InCluster(deployment string) (*Scaler, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("not running in Kubernetes (KUBERNETES_SERVICE_HOST is unset)")
	}
	ns, err := os.ReadFile(saDir + "/namespace")
	if err != nil {
		return nil, fmt.Errorf("service account (automountServiceAccountToken?): %w", err)
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("service account CA: no certificate")
	}
	return New("https://"+net.JoinHostPort(host, port), strings.TrimSpace(string(ns)), deployment,
		saDir+"/token", &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		}), nil
}

// New returns a scaler talking to the API at base (for tests).
func New(base, namespace, deployment, tokenFile string, c *http.Client) *Scaler {
	return &Scaler{base: strings.TrimRight(base, "/"), namespace: namespace, deployment: deployment,
		tokenFile: tokenFile, http: c}
}

// Deployment names the scaled Deployment.
func (s *Scaler) Deployment() string { return s.deployment }

// Scale sets the Deployment's replicas.
func (s *Scaler) Scale(ctx context.Context, replicas int) error {
	body, err := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	if err != nil {
		return err
	}
	_, err = s.do(ctx, http.MethodPatch, "/scale", "application/merge-patch+json", body)
	return err
}

// Status is how many replicas a Deployment wants and has ready.
type Status struct {
	Replicas, Ready int
}

// Status reads the Deployment's wanted and ready replicas.
func (s *Scaler) Status(ctx context.Context) (Status, error) {
	data, err := s.do(ctx, http.MethodGet, "", "", nil)
	if err != nil {
		return Status{}, err
	}
	var d struct {
		Spec struct {
			Replicas *int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return Status{}, fmt.Errorf("deployment %s: %w", s.deployment, err)
	}
	st := Status{Replicas: 1, Ready: d.Status.ReadyReplicas}
	if d.Spec.Replicas != nil {
		st.Replicas = *d.Spec.Replicas
	}
	return st, nil
}

func (s *Scaler) do(ctx context.Context, method, sub, contentType string, body []byte) ([]byte, error) {
	token, err := os.ReadFile(s.tokenFile) // rotated by the kubelet: read every time
	if err != nil {
		return nil, fmt.Errorf("service account token: %w", err)
	}
	url := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments/%s%s", s.base, s.namespace, s.deployment, sub)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deployment %s: %s: %s", s.deployment, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}
