package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/spf13/cobra"
)

var licenseCmd = &cobra.Command{
	Use:   "license",
	Short: "Manage and activate your missionctl Bundle license via Polar.sh",
	Long:  `Activate or validate your missionctl Bundle license key directly with Polar.sh. Unlocks AI categorization and recurring-payment detection.`,
}

type licenseActivateRequest struct {
	Key            string `json:"key"`
	OrganizationID string `json:"organization_id"`
	Label          string `json:"label"`
}

type licenseValidateRequest struct {
	Key            string `json:"key"`
	OrganizationID string `json:"organization_id"`
}

type polarError struct {
	Error  string      `json:"error"`
	Detail interface{} `json:"detail"`
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", len(s)-4)
}

var licenseActivateCmd = &cobra.Command{
	Use:   "activate <key>",
	Short: "Activate your license key on this machine",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		key := strings.TrimSpace(args[0])
		orgID := config.PolarOrgID()

		hostname, err := os.Hostname()
		if err != nil {
			hostname = "budgetctl-terminal"
		}
		label := fmt.Sprintf("%s (%s)", hostname, time.Now().Format("02.01.2006"))

		reqBody := licenseActivateRequest{Key: key, OrganizationID: orgID, Label: label}
		jsonBytes, err := json.Marshal(reqBody)
		if err != nil {
			fmt.Printf("✗ Internal error: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Activating license with Polar.sh...")

		resp, err := http.Post("https://api.polar.sh/v1/customer-portal/license-keys/activate", "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			fmt.Printf("✗ Network error: Could not reach Polar.sh API (%v)\n", err)
			fmt.Println("  Key registered locally, will verify once online.")
			_ = config.SetLicense(key, "offline_pending")
			os.Exit(0)
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			fmt.Println("✓ License activated! AI categorization and recurring-payment detection unlocked.")
			if err := config.SetLicense(key, "active"); err != nil {
				fmt.Printf("✗ Error saving configuration: %v\n", err)
				os.Exit(1)
			}
		} else {
			var polarErr polarError
			_ = json.Unmarshal(bodyBytes, &polarErr)
			errMsg := "Invalid or inactive key"
			if polarErr.Error != "" {
				errMsg = polarErr.Error
			}
			fmt.Printf("✗ Activation failed: %s (Status: %d)\n", errMsg, resp.StatusCode)
			_ = config.SetLicense(key, "invalid")
			os.Exit(1)
		}
	},
}

var licenseStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the current activation status of your license key",
	Run: func(cmd *cobra.Command, args []string) {
		key := config.LicenseKey()
		if key == "" {
			fmt.Println("License Type: CORE (free)")
			fmt.Println("Status: No license registered. Get the Bundle at https://missionctl.sh/#pricing")
			fmt.Println("Then run: budgetctl license activate <key>")
			return
		}

		reqBody := licenseValidateRequest{Key: key, OrganizationID: config.PolarOrgID()}
		jsonBytes, _ := json.Marshal(reqBody)

		resp, err := http.Post("https://api.polar.sh/v1/customer-portal/license-keys/validate", "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			fmt.Println("License Type: PRO (Offline)")
			fmt.Printf("License Key:  %s\n", maskSecret(key))
			fmt.Printf("Status:       %s (Verification offline, cached status used)\n", strings.ToUpper(strings.TrimSpace(config.LicenseStatus())))
			return
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)

		if resp.StatusCode == http.StatusOK {
			var valResp struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(bodyBytes, &valResp)
			status := "active"
			if valResp.Status != "" {
				status = valResp.Status
			}
			fmt.Println("License Type: PRO")
			fmt.Printf("License Key:  %s\n", maskSecret(key))
			fmt.Printf("Status:       %s (Verified with Polar.sh)\n", strings.ToUpper(status))
			_ = config.SetLicense(key, status)
		} else {
			fmt.Println("License Type: INVALID / EXPIRED")
			fmt.Printf("License Key:  %s\n", maskSecret(key))
			fmt.Printf("Status:       %d (Server returned invalid license status)\n", resp.StatusCode)
			_ = config.SetLicense(key, "invalid")
		}
	},
}

func init() {
	licenseCmd.AddCommand(licenseActivateCmd)
	licenseCmd.AddCommand(licenseStatusCmd)
	rootCmd.AddCommand(licenseCmd)
}
