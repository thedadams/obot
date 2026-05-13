package license

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	keygen "github.com/keygen-sh/keygen-go/v3"
	"github.com/obot-platform/obot/logger"
)

const (
	// EnterpriseAuthProvidersEntitlement is required to enable enterprise auth providers.
	EnterpriseAuthProvidersEntitlement = "OBOT_ENTERPRISE_AUTH_PROVIDERS"

	// EnterpriseModelProvidersEntitlement is required to enable enterprise model providers.
	EnterpriseModelProvidersEntitlement = "OBOT_ENTERPRISE_MODEL_PROVIDERS"

	defaultPollInterval = time.Hour
	keygenProduct       = "18a762f2-5281-45cf-93fc-e45e2d932094"
	keygenAccount       = "7565373b-6069-4a0b-9495-9777d9db3fd9"
)

var (

	// ErrNotConfigured indicates license validation was requested without enough Keygen configuration.
	ErrNotConfigured = errors.New("license provider is not configured")

	log = logger.Package()
)

// Config contains the Keygen settings needed to validate an Obot license.
type Config struct {
	KeygenLicenseKey string `usage:"Keygen license key for this Obot installation"`
}

type KeygenProvider struct {
	entitlementsLock, licenseKeyLock sync.RWMutex
	entitlements                     map[keygen.EntitlementCode]struct{}
	machineFingerprint               string
}

// NewProvider creates a Keygen-backed license provider.
func NewProvider(ctx context.Context, machineFingerPrint string, config Config) (*KeygenProvider, error) {
	keygen.Account = keygenAccount
	keygen.Product = keygenProduct
	if licenseKey := strings.TrimSpace(config.KeygenLicenseKey); licenseKey != "" {
		keygen.LicenseKey = licenseKey
	} else {
		log.Infof("license provider is not configured, license key is empty")
		return nil, nil
	}

	k := &KeygenProvider{
		machineFingerprint: strings.TrimSpace(machineFingerPrint),
	}

	var err error
	k.entitlements, err = k.validate(ctx)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return nil, err
	}

	go k.poll(ctx)

	return k, nil
}

func (p *KeygenProvider) validate(ctx context.Context) (map[keygen.EntitlementCode]struct{}, error) {
	if err := validateConfig(); err != nil {
		return nil, err
	}

	lic, err := keygen.Validate(ctx, p.machineFingerprint)
	if err != nil {
		if lic != nil && lic.LastValidation != nil && lic.LastValidation.Code == keygen.ValidationCodeNoMachine {
			if _, activationErr := lic.Activate(ctx, p.machineFingerprint); activationErr != nil && !errors.Is(activationErr, keygen.ErrMachineAlreadyActivated) {
				log.Warnf("license activation failed: %v", activationErr)
				return nil, nil
			}

			lic, err = keygen.Validate(ctx, p.machineFingerprint)
		}
		if err != nil {
			log.Warnf("license validation failed: %v", err)
			return nil, nil
		}
	}

	entitlements, err := lic.Entitlements(ctx)
	if err != nil {
		return nil, fmt.Errorf("list license entitlements: %w", err)
	}

	entitlementSet := make(map[keygen.EntitlementCode]struct{}, len(entitlements))
	for _, entitlement := range entitlements {
		entitlementSet[entitlement.Code] = struct{}{}
	}

	return entitlementSet, nil
}

func (p *KeygenProvider) HasValidLicense() bool {
	if p == nil {
		return false
	}

	p.entitlementsLock.RLock()
	defer p.entitlementsLock.RUnlock()

	return p.entitlements != nil
}

func (p *KeygenProvider) Entitlements() []string {
	if p == nil {
		return nil
	}

	p.entitlementsLock.RLock()
	defer p.entitlementsLock.RUnlock()

	if p.entitlements == nil {
		return nil
	}

	entitlements := make([]string, 0, len(p.entitlements))
	for entitlement := range p.entitlements {
		entitlements = append(entitlements, string(entitlement))
	}

	slices.Sort(entitlements)

	return entitlements
}

func (p *KeygenProvider) HasEntitlement(key string) bool {
	if p == nil {
		return false
	}

	p.entitlementsLock.RLock()
	defer p.entitlementsLock.RUnlock()

	_, ok := p.entitlements[keygen.EntitlementCode(key)]
	return ok
}

func (p *KeygenProvider) poll(ctx context.Context) {
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.update(ctx)
		}
	}
}

func (p *KeygenProvider) update(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var (
		entitlements  map[keygen.EntitlementCode]struct{}
		hasLicenceKey bool
		err           error
	)

	p.licenseKeyLock.RLock()
	if keygen.LicenseKey != "" {
		hasLicenceKey = true
		entitlements, err = p.validate(ctx)
	}
	p.licenseKeyLock.RUnlock()

	p.entitlementsLock.Lock()
	defer p.entitlementsLock.Unlock()

	if err != nil || !hasLicenceKey {
		p.entitlements = nil
		return
	}

	p.entitlements = entitlements
}

func validateConfig() error {
	if strings.TrimSpace(keygen.Account) == "" {
		return fmt.Errorf("%w: missing Keygen account", ErrNotConfigured)
	}
	if strings.TrimSpace(keygen.Product) == "" {
		return fmt.Errorf("%w: missing Keygen product", ErrNotConfigured)
	}
	if strings.TrimSpace(keygen.LicenseKey) == "" {
		return fmt.Errorf("%w: missing license key or token", ErrNotConfigured)
	}
	return nil
}
