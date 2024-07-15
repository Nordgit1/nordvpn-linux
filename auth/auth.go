/*
Package auth is responsible for user authentication.
*/
package auth

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/NordSecurity/nordvpn-linux/config"
	"github.com/NordSecurity/nordvpn-linux/core"
	"github.com/NordSecurity/nordvpn-linux/internal"
)

// How much time dedicated IP servers list is valid, before making a new API call
const cacheValidityForDIPServer = 10 * time.Minute

// Checker provides information about current authentication.
type Checker interface {
	// IsLoggedIn returns true when the user is logged in.
	IsLoggedIn() bool
	// IsVPNExpired is used to check whether the user is allowed to use VPN
	IsVPNExpired() (bool, error)
	// IsDedicatedIPExpired is used to check whether the user is allowed to use dedicated IP servers
	IsDedicatedIPExpired() (bool, error)
	// Get the service data if the user has it and nil otherwise
	ServiceData(serviceID int64) (*config.ServiceData, error)
}

const (
	VPNServiceID         = 1
	DedicatedIPServiceID = 11
)

// RenewingChecker does both authentication checks and renewals in case of expiration.
type RenewingChecker struct {
	cm    config.Manager
	creds core.CredentialsAPI
	mu    sync.Mutex
}

// NewRenewingChecker is a default constructor for RenewingChecker.
func NewRenewingChecker(cm config.Manager, creds core.CredentialsAPI) *RenewingChecker {
	return &RenewingChecker{cm: cm, creds: creds}
}

// IsLoggedIn reports user login status.
//
// Thread safe.
func (r *RenewingChecker) IsLoggedIn() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	var cfg config.Config
	if err := r.cm.Load(&cfg); err != nil {
		return false
	}

	isLoggedIn := true
	for uid, data := range cfg.TokensData {
		if err := r.renew(uid, data); err != nil {
			isLoggedIn = false
		}
	}

	return cfg.AutoConnectData.ID != 0 && len(cfg.TokensData) > 0 && isLoggedIn
}

// IsVPNExpired is used to check whether the user is allowed to use VPN
func (r *RenewingChecker) IsVPNExpired() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var cfg config.Config
	if err := r.cm.Load(&cfg); err != nil {
		return true, fmt.Errorf("loading config: %w", err)
	}

	data := cfg.TokensData[cfg.AutoConnectData.ID]
	if isTokenExpired(data.ServiceExpiry) {
		if err := r.fetchServices(cfg.AutoConnectData.ID, &data); err != nil {
			return true, fmt.Errorf("updating service expiry token: %w", err)
		}
		if err := r.cm.SaveWith(saveVpnExpirationDate(cfg.AutoConnectData.ID, data)); err != nil {
			return true, fmt.Errorf("saving config: %w", err)
		}
	}

	return isTokenExpired(data.ServiceExpiry), nil
}

// IsDedicatedIPExpired is used to check whether the user is allowed to use dedicated IP servers
func (r *RenewingChecker) IsDedicatedIPExpired() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var cfg config.Config
	if err := r.cm.Load(&cfg); err != nil {
		return true, fmt.Errorf("loading config: %w", err)
	}

	data := cfg.TokensData[cfg.AutoConnectData.ID]
	if isTokenExpired(data.DedicatedIPExpiry) {
		if err := r.fetchServices(cfg.AutoConnectData.ID, &data); err != nil {
			return true, fmt.Errorf("updating service expiry token: %w", err)
		}
		if err := r.cm.SaveWith(saveVpnExpirationDate(cfg.AutoConnectData.ID, data)); err != nil {
			return true, fmt.Errorf("saving config: %w", err)
		}
	}

	return isTokenExpired(data.DedicatedIPExpiry), nil
}

func (r *RenewingChecker) ServiceData(serviceID int64) (*config.ServiceData, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var cfg config.Config
	if err := r.cm.Load(&cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	data := cfg.TokensData[cfg.AutoConnectData.ID]
	for _, serviceData := range data.Services.ServicesData {
		if serviceData.Service.ID == serviceID {
			if isTokenExpired(serviceData.ExpiresAt) ||
				(serviceID == DedicatedIPServiceID &&
					time.Now().After(data.Services.CachedDate.Add(getDipCacheValidity()))) {
				// for DIP refresh the service because the servers list can change
				if err := r.fetchServices(cfg.AutoConnectData.ID, &data); err != nil {
					return nil, fmt.Errorf("updating service expiry token: %w", err)
				}
			}

			return &serviceData, nil
		}
	}

	return nil, fmt.Errorf("service not found")
}

func (r *RenewingChecker) renew(uid int64, data config.TokenData) error {
	// We are renewing token if it is expired because we need to make some API calls later
	if isTokenExpired(data.TokenExpiry) {
		if err := r.renewLoginToken(&data); err != nil {
			if errors.Is(err, core.ErrUnauthorized) ||
				errors.Is(err, core.ErrNotFound) ||
				errors.Is(err, core.ErrBadRequest) {
				return r.cm.SaveWith(Logout(uid))
			}
			return nil
		}
		// We renew NC credentials along the login token
		if err := r.renewNCCredentials(&data); err != nil {
			if errors.Is(err, core.ErrUnauthorized) ||
				errors.Is(err, core.ErrNotFound) ||
				errors.Is(err, core.ErrBadRequest) {
				return r.cm.SaveWith(Logout(uid))
			}
			return nil
		}
		if data.IsOAuth {
			if err := r.renewTrustedPassToken(&data); err != nil {
				if errors.Is(err, core.ErrUnauthorized) ||
					errors.Is(err, core.ErrNotFound) ||
					errors.Is(err, core.ErrBadRequest) {
					return r.cm.SaveWith(Logout(uid))
				}
			}
		}
		if err := r.cm.SaveWith(saveLoginToken(uid, data)); err != nil {
			return err
		}
	}

	// TrustedPass was introduced later on, so it's possible that valid data is not stored even though renew token
	// is still valid. In such cases we need to hit the api to get the initial value.
	isTrustedPassNotValid := (data.TrustedPassToken == "" || data.TrustedPassOwnerID == "")
	// TrustedPass is viable only in case of OAuth login.
	if data.IsOAuth && isTrustedPassNotValid {
		if err := r.renewTrustedPassToken(&data); err != nil {
			if errors.Is(err, core.ErrUnauthorized) ||
				errors.Is(err, core.ErrNotFound) ||
				errors.Is(err, core.ErrBadRequest) {
				return r.cm.SaveWith(Logout(uid))
			}
		}

		if err := r.cm.SaveWith(saveLoginToken(uid, data)); err != nil {
			return err
		}
	}

	if data.NordLynxPrivateKey == "" ||
		data.OpenVPNUsername == "" || data.OpenVPNPassword == "" {
		if err := r.renewVpnCredentials(&data); err != nil {
			return err
		}
		if err := r.cm.SaveWith(saveVpnServerCredentials(uid, data)); err != nil {
			return err
		}
	}

	return nil
}

func (r *RenewingChecker) renewLoginToken(data *config.TokenData) error {
	resp, err := r.creds.TokenRenew(data.RenewToken)
	if err != nil {
		return err
	}

	data.Token = resp.Token
	data.RenewToken = resp.RenewToken
	data.TokenExpiry = resp.ExpiresAt
	return nil
}

func (r *RenewingChecker) renewNCCredentials(data *config.TokenData) error {
	resp, err := r.creds.NotificationCredentials(data.Token, data.NCData.UserID.String())
	if err != nil {
		return err
	}

	data.NCData.Endpoint = resp.Endpoint
	data.NCData.Username = resp.Username
	data.NCData.Password = resp.Password
	return nil
}

func (r *RenewingChecker) renewTrustedPassToken(data *config.TokenData) error {
	resp, err := r.creds.TrustedPassToken(data.Token)
	if err != nil {
		return fmt.Errorf("getting trusted pass token data: %w", err)
	}

	data.TrustedPassOwnerID = resp.OwnerID
	data.TrustedPassToken = resp.Token

	return nil
}

func (r *RenewingChecker) renewVpnCredentials(data *config.TokenData) error {
	credentials, err := r.creds.ServiceCredentials(data.Token)
	if err != nil {
		return err
	}

	data.NordLynxPrivateKey = credentials.NordlynxPrivateKey
	data.OpenVPNUsername = credentials.Username
	data.OpenVPNPassword = credentials.Password
	return nil
}

func (r *RenewingChecker) fetchServices(userId int64, data *config.TokenData) error {
	services, err := r.creds.Services(data.Token)
	if err != nil {
		return err
	}

	for _, service := range services {
		if service.Service.ID == VPNServiceID { // VPN service
			data.ServiceExpiry = service.ExpiresAt
		}

		if service.Service.ID == DedicatedIPServiceID {
			data.DedicatedIPExpiry = service.ExpiresAt
		}
	}

	data.Services = config.Services{
		CachedDate:   time.Now(),
		ServicesData: services,
	}

	if err := r.cm.SaveWith(saveVpnExpirationDate(userId, *data)); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	return nil
}

// saveLoginToken persists only token related data,
// it does not touch vpn specific data.
func saveLoginToken(userID int64, data config.TokenData) config.SaveFunc {
	return func(c config.Config) config.Config {
		user := c.TokensData[userID]
		defer func() { c.TokensData[userID] = user }()

		user.Token = data.Token
		user.RenewToken = data.RenewToken
		user.TokenExpiry = data.TokenExpiry
		user.NCData.Endpoint = data.NCData.Endpoint
		user.NCData.Username = data.NCData.Username
		user.NCData.Password = data.NCData.Password
		user.TrustedPassOwnerID = data.TrustedPassOwnerID
		user.TrustedPassToken = data.TrustedPassToken
		return c
	}
}

func saveVpnExpirationDate(userID int64, data config.TokenData) config.SaveFunc {
	return func(c config.Config) config.Config {
		user := c.TokensData[userID]
		defer func() { c.TokensData[userID] = user }()

		user.ServiceExpiry = data.ServiceExpiry
		user.DedicatedIPExpiry = data.DedicatedIPExpiry
		user.Services = data.Services
		return c
	}
}

func saveVpnServerCredentials(userID int64, data config.TokenData) config.SaveFunc {
	return func(c config.Config) config.Config {
		user := c.TokensData[userID]
		defer func() { c.TokensData[userID] = user }()

		user.NordLynxPrivateKey = data.NordLynxPrivateKey
		user.OpenVPNUsername = data.OpenVPNUsername
		user.OpenVPNPassword = data.OpenVPNPassword
		return c
	}
}

// Logout the user.
func Logout(user int64) config.SaveFunc {
	return func(c config.Config) config.Config {
		delete(c.TokensData, user)
		return c
	}
}

// isTokenExpired reports whether the token is expired or not.
func isTokenExpired(expiryTime string) bool {
	if expiryTime == "" {
		return true
	}

	expiry, err := time.Parse(internal.ServerDateFormat, expiryTime)
	if err != nil {
		return true
	}

	return time.Now().After(expiry)
}

func getDipCacheValidity() time.Duration {
	if validity := os.Getenv("DIP_CACHE_VALIDITY"); validity != "" {
		if duration, err := time.ParseDuration(validity); err != nil {
			log.Println(internal.WarningPrefix, "cannot convert env DIP cache duration", validity, err)
		} else {
			return duration
		}
	}

	return cacheValidityForDIPServer
}
