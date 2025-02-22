// Copyright 2023 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package externalcloudaudit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/sirupsen/logrus"

	"github.com/gravitational/teleport/api/types/externalcloudaudit"
	"github.com/gravitational/teleport/lib/modules"
	"github.com/gravitational/teleport/lib/services"
)

const (
	// TokenLifetime is the lifetime of OIDC tokens used by the
	// ExternalCloudAudit service with the AWS OIDC integration.
	TokenLifetime = time.Hour

	refreshBeforeExpirationPeriod = 15 * time.Minute
	refreshCheckInterval          = 30 * time.Second
	retrieveTimeout               = 30 * time.Second
)

// Configurator provides functionality necessary for configuring the External
// Cloud Audit feature.
//
// Specifically:
//   - IsUsed() reports whether the feature is currently activated and in use.
//   - GetSpec() provides the current cluster ExternalCloudAuditSpec
//   - CredentialsProvider() provides AWS credentials for the necessary customer
//     resources that can be used with aws-sdk-go-v2
//   - CredentialsProviderSDKV1() provides AWS credentials for the necessary customer
//     resources that can be used with aws-sdk-go
//
// Configurator is a dependency to both the S3 session uploader and the Athena
// audit logger. They are both initialized before Auth. However, Auth needs to
// be initialized in order to provide signatures for the OIDC tokens.  That's
// why SetGenerateOIDCTokenFn() must be called after auth is initialized to inject
// the OIDC token source dynamically.
//
// If auth needs to emit any events during initialization (before
// SetGenerateOIDCTokenFn is called) that is okay. Events are written to
// SQS first, credentials from the Configurator are not needed until the batcher
// reads the events from SQS and tries to write a batch to the customer S3
// bucket. If the batcher tries to write a batch before the Configurator is
// initialized and gets an error when trying to retrieve credentials, that's
// still okay, it will always retry.
type Configurator struct {
	// spec is set during initialization of the Configurator. It won't
	// change, because every change of spec triggers an Auth service reload.
	spec   *externalcloudaudit.ExternalCloudAuditSpec
	isUsed bool

	credentialsCache *credentialsCache
}

// Options holds options for the Configurator.
type Options struct {
	clock     clockwork.Clock
	stsClient stscreds.AssumeRoleWithWebIdentityAPIClient
}

func (o *Options) setDefaults(ctx context.Context, region string) error {
	if o.clock == nil {
		o.clock = clockwork.NewRealClock()
	}
	if o.stsClient == nil {
		var useFips aws.FIPSEndpointState
		if modules.GetModules().IsBoringBinary() {
			useFips = aws.FIPSEndpointStateEnabled
		}
		cfg, err := config.LoadDefaultConfig(
			ctx,
			config.WithRegion(region),
			config.WithUseFIPSEndpoint(useFips),
			config.WithRetryMaxAttempts(10),
		)
		if err != nil {
			return trace.Wrap(err)
		}
		o.stsClient = sts.NewFromConfig(cfg)
	}
	return nil
}

// WithClock is a functional option to set the clock.
func WithClock(clock clockwork.Clock) func(*Options) {
	return func(opts *Options) {
		opts.clock = clock
	}
}

// WithSTSClient is a functional option to set the sts client.
func WithSTSClient(clt stscreds.AssumeRoleWithWebIdentityAPIClient) func(*Options) {
	return func(opts *Options) {
		opts.stsClient = clt
	}
}

// NewConfigurator returns a new Configurator set up with the current active
// cluster ExternalCloudAudit spec from [ecaSvc].
//
// If the External Cloud Audit feature is not used in this cluster then a valid
// instance will be returned where IsUsed() will return false.
func NewConfigurator(ctx context.Context, ecaSvc services.ExternalCloudAuditGetter, integrationSvc services.IntegrationsGetter, optFns ...func(*Options)) (*Configurator, error) {
	active, err := ecaSvc.GetClusterExternalCloudAudit(ctx)
	if err != nil {
		if trace.IsNotFound(err) {
			return &Configurator{isUsed: false}, nil
		}
		return nil, trace.Wrap(err)
	}
	return newConfigurator(ctx, &active.Spec, integrationSvc, optFns...)
}

// NewDraftConfigurator is equivalent to NewConfigurator but is based on the
// current *draft* ExternalCloudAudit configuration instead of the active
// configuration.
//
// If a draft ExternalCloudAudit configuration is not found, an error will be
// returned.
func NewDraftConfigurator(ctx context.Context, ecaSvc services.ExternalCloudAuditGetter, integrationSvc services.IntegrationsGetter, optFns ...func(*Options)) (*Configurator, error) {
	draft, err := ecaSvc.GetDraftExternalCloudAudit(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return newConfigurator(ctx, &draft.Spec, integrationSvc, optFns...)
}

func newConfigurator(ctx context.Context, spec *externalcloudaudit.ExternalCloudAuditSpec, integrationSvc services.IntegrationsGetter, optFns ...func(*Options)) (*Configurator, error) {
	// ExternalCloudAudit is only available in Cloud Enterprise
	// (IsUsageBasedBilling indicates Teleport Team, where this is not supported)
	if !modules.GetModules().Features().Cloud || modules.GetModules().Features().IsUsageBasedBilling {
		return &Configurator{isUsed: false}, nil
	}

	oidcIntegrationName := spec.IntegrationName
	integration, err := integrationSvc.GetIntegration(ctx, oidcIntegrationName)
	if err != nil {
		if trace.IsNotFound(err) {
			return nil, trace.NotFound(
				"ExternalCloudAudit: configured AWS OIDC integration %q not found",
				oidcIntegrationName)
		}
	}
	awsOIDCSpec := integration.GetAWSOIDCIntegrationSpec()
	if awsOIDCSpec == nil {
		return nil, trace.NotFound(
			"ExternalCloudAudit: configured integration %q does not appear to be an AWS OIDC integration",
			oidcIntegrationName)
	}
	awsRoleARN := awsOIDCSpec.RoleARN

	options := &Options{}
	for _, optFn := range optFns {
		optFn(options)
	}
	if err := options.setDefaults(ctx, spec.Region); err != nil {
		return nil, trace.Wrap(err)
	}

	credentialsCache, err := newCredentialsCache(ctx, spec.Region, awsRoleARN, options)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	go credentialsCache.run(ctx)

	return &Configurator{
		isUsed:           true,
		spec:             spec,
		credentialsCache: credentialsCache,
	}, nil
}

// IsUsed returns a boolean indicating whether the ExternalCloudAudit feature is
// currently in active use.
func (c *Configurator) IsUsed() bool {
	return c.isUsed
}

// GetSpec returns the current active ExternalCloudAuditSpec.
func (c *Configurator) GetSpec() *externalcloudaudit.ExternalCloudAuditSpec {
	return c.spec
}

// GenerateOIDCTokenFn is a function that should return a valid, signed JWT for
// authenticating to AWS via OIDC.
type GenerateOIDCTokenFn func(ctx context.Context) (string, error)

// SetGenerateOIDCTokenFn sets the source of OIDC tokens for this Configurator.
func (c *Configurator) SetGenerateOIDCTokenFn(fn GenerateOIDCTokenFn) {
	c.credentialsCache.setGenerateOIDCTokenFn(fn)
}

// CredentialsProvider returns an aws.CredentialsProvider that can be used to
// authenticate with the customer AWS account via the configured AWS OIDC
// integration with aws-sdk-go-v2.
func (p *Configurator) CredentialsProvider() aws.CredentialsProvider {
	return p.credentialsCache
}

// CredentialsProviderSDKV1 returns a credentials.ProviderWithContext that can be used to
// authenticate with the customer AWS account via the configured AWS OIDC
// integration with aws-sdk-go.
func (p *Configurator) CredentialsProviderSDKV1() credentials.ProviderWithContext {
	return &v1Adapter{cc: p.credentialsCache}
}

// WaitForFirstCredentials waits for the internal credentials cache to finish
// fetching its first credentials (or getting an error attempting to do so).
// This can be called after SetGenerateOIDCTokenFn to make sure any returned
// credential providers won't return errors simply due to the cache not being
// ready yet.
func (p *Configurator) WaitForFirstCredentials(ctx context.Context) {
	p.credentialsCache.waitForFirstCredsOrErr(ctx)
}

// credentialsCache is used to store and refresh AWS credentials used with
// AWS OIDC integration.
//
// Credentials are valid for 1h, but they cannot be refreshed if Proxy is down,
// so we attempt to refresh the credentials early and retry on failure.
//
// credentialsCache is a dependency to both the s3 session uploader and the
// athena audit logger. They are both initialized before auth. However AWS
// credentials using OIDC integration can be obtained only after auth is
// initialized. That's why generateOIDCTokenFn is injected dynamically after
// auth is initialized. Before initialization, credentialsCache will return
// an error on any Retrieve call.
type credentialsCache struct {
	log *logrus.Entry

	roleARN string

	// generateOIDCTokenFn is dynamically set after auth is initialized.
	generateOIDCTokenFn GenerateOIDCTokenFn

	// initialized communicates (via closing channel) that generateOIDCTokenFn is set.
	initialized      chan struct{}
	closeInitialized func()

	// gotFirstCredsOrErr communicates (via closing channel) that the first
	// credsOrErr has been set.
	gotFirstCredsOrErr      chan struct{}
	closeGotFirstCredsOrErr func()

	credsOrErr   credsOrErr
	credsOrErrMu sync.RWMutex

	stsClient stscreds.AssumeRoleWithWebIdentityAPIClient
	clock     clockwork.Clock
}

type credsOrErr struct {
	creds aws.Credentials
	err   error
}

func newCredentialsCache(ctx context.Context, region, roleARN string, options *Options) (*credentialsCache, error) {
	initialized := make(chan struct{})
	gotFirstCredsOrErr := make(chan struct{})
	return &credentialsCache{
		roleARN:                 roleARN,
		log:                     logrus.WithField(trace.Component, "ExternalCloudAudit.CredentialsCache"),
		initialized:             initialized,
		closeInitialized:        sync.OnceFunc(func() { close(initialized) }),
		gotFirstCredsOrErr:      gotFirstCredsOrErr,
		closeGotFirstCredsOrErr: sync.OnceFunc(func() { close(gotFirstCredsOrErr) }),
		credsOrErr: credsOrErr{
			err: errors.New("ExternalCloudAudit: credential cache not yet initialized"),
		},
		clock:     options.clock,
		stsClient: options.stsClient,
	}, nil
}

func (cc *credentialsCache) setGenerateOIDCTokenFn(fn GenerateOIDCTokenFn) {
	cc.generateOIDCTokenFn = fn
	cc.closeInitialized()
}

// Retrieve implements [aws.CredentialsProvider] and returns the latest cached
// credentials, or an error if no credentials have been generated yet or the
// last generated credentials have expired.
func (cc *credentialsCache) Retrieve(ctx context.Context) (aws.Credentials, error) {
	cc.credsOrErrMu.RLock()
	defer cc.credsOrErrMu.RUnlock()
	return cc.credsOrErr.creds, cc.credsOrErr.err
}

func (cc *credentialsCache) run(ctx context.Context) {
	// Wait for initialized signal before running loop.
	select {
	case <-cc.initialized:
	case <-ctx.Done():
		cc.log.Debug("Context canceled before initialized.")
		return
	}

	cc.refreshIfNeeded(ctx)

	ticker := cc.clock.NewTicker(refreshCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Chan():
			cc.refreshIfNeeded(ctx)
		case <-ctx.Done():
			cc.log.Debugf("Context canceled, stopping refresh loop.")
			return
		}
	}
}

func (cc *credentialsCache) refreshIfNeeded(ctx context.Context) {
	credsFromCache, err := cc.Retrieve(ctx)
	if err == nil &&
		credsFromCache.HasKeys() &&
		cc.clock.Now().Add(refreshBeforeExpirationPeriod).Before(credsFromCache.Expires) {
		// No need to refresh, credentials in cache are still valid for longer
		// than refreshBeforeExpirationPeriod
		return
	}
	cc.log.Debugf("Refreshing credentials.")

	creds, err := cc.refresh(ctx)
	if err != nil {
		// If we were not able to refresh, check if existing credentials in cache are still valid.
		// If yes, just log debug, it will be retried on next interval check.
		if credsFromCache.HasKeys() && cc.clock.Now().Before(credsFromCache.Expires) {
			cc.log.Warnf("Failed to retrieve new credentials: %v", err)
			cc.log.Debugf("Using existing credentials expiring in %s.", credsFromCache.Expires.Sub(cc.clock.Now()).Round(time.Second).String())
			return
		}
		// If existing creds are expired, update cached error.
		cc.setCredsOrErr(credsOrErr{err: trace.Wrap(err)})
		return
	}
	// Refresh went well, update cached creds.
	cc.setCredsOrErr(credsOrErr{creds: creds})
	cc.log.Debugf("Successfully refreshed credentials, new expiry at %v", creds.Expires)
}

func (cc *credentialsCache) setCredsOrErr(coe credsOrErr) {
	cc.credsOrErrMu.Lock()
	defer cc.credsOrErrMu.Unlock()
	cc.credsOrErr = coe
	cc.closeGotFirstCredsOrErr()
}

func (cc *credentialsCache) refresh(ctx context.Context) (aws.Credentials, error) {
	oidcToken, err := cc.generateOIDCTokenFn(ctx)
	if err != nil {
		return aws.Credentials{}, trace.Wrap(err)
	}

	roleProvider := stscreds.NewWebIdentityRoleProvider(
		cc.stsClient,
		cc.roleARN,
		identityToken(oidcToken),
		func(wiro *stscreds.WebIdentityRoleOptions) {
			wiro.Duration = TokenLifetime
		},
	)

	ctx, cancel := context.WithTimeout(ctx, retrieveTimeout)
	defer cancel()

	creds, err := roleProvider.Retrieve(ctx)
	return creds, trace.Wrap(err)
}

func (cc *credentialsCache) waitForFirstCredsOrErr(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-cc.gotFirstCredsOrErr:
	}
}

// identityToken is an implementation of [stscreds.IdentityTokenRetriever] for returning a static token.
type identityToken string

// GetIdentityToken returns the token configured.
func (j identityToken) GetIdentityToken() ([]byte, error) {
	return []byte(j), nil
}

// v1Adapter wraps the credentialsCache to implement
// [credentials.ProviderWithContext] used by aws-sdk-go (v1).
type v1Adapter struct {
	cc *credentialsCache
}

var _ credentials.ProviderWithContext = (*v1Adapter)(nil)

// RetrieveWithContext returns cached credentials.
func (a *v1Adapter) RetrieveWithContext(ctx context.Context) (credentials.Value, error) {
	credsV2, err := a.cc.Retrieve(ctx)
	if err != nil {
		return credentials.Value{}, trace.Wrap(err)
	}

	return credentials.Value{
		AccessKeyID:     credsV2.AccessKeyID,
		SecretAccessKey: credsV2.SecretAccessKey,
		SessionToken:    credsV2.SessionToken,
		ProviderName:    credsV2.Source,
	}, nil
}

// Retrieve returns cached credentials.
func (a *v1Adapter) Retrieve() (credentials.Value, error) {
	return a.RetrieveWithContext(context.Background())
}

// IsExpired always returns true in order to opt out of AWS SDK credential
// caching. Retrieve(WithContext) already returns cached credentials.
func (a *v1Adapter) IsExpired() bool {
	return true
}
