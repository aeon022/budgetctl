package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/missionctl-core/licensing"
	"github.com/spf13/cobra"
)

var licenseCmd = &cobra.Command{
	Use:   "license",
	Short: "Manage and activate your missionctl Bundle license via Polar.sh",
	Long:  `Activate or validate your missionctl Bundle license key directly with Polar.sh. Unlocks AI categorization and recurring-payment detection.`,
}

var licenseActivateCmd = &cobra.Command{
	Use:   "activate <key>",
	Short: "Activate your license key on this machine",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		key := strings.TrimSpace(args[0])
		orgID := config.PolarOrgID()
		label := licensing.Label("budgetctl")

		fmt.Println("Activating license with Polar.sh...")

		result, err := licensing.Activate(orgID, key, label)
		if err != nil {
			if result.Status == "offline_pending" {
				fmt.Printf("✗ Network error: Could not reach Polar.sh API (%v)\n", err)
				fmt.Println("  Key registered locally, will verify once online.")
				_ = config.SetLicense(key, "offline_pending", "")
				os.Exit(0)
			}
			fmt.Printf("✗ Activation failed: %v\n", err)
			_ = config.SetLicense(key, "invalid", "")
			os.Exit(1)
		}

		fmt.Println("✓ License activated! AI categorization and recurring-payment detection unlocked.")
		if err := config.SetLicense(key, result.Status, result.BenefitID); err != nil {
			fmt.Printf("✗ Error saving configuration: %v\n", err)
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

		result, err := licensing.Validate(config.PolarOrgID(), key)
		if err != nil {
			if result.Status == "" {
				fmt.Println("License Type: PRO (Offline)")
				fmt.Printf("License Key:  %s\n", licensing.MaskKey(key))
				fmt.Printf("Status:       %s (Verification offline, cached status used)\n", strings.ToUpper(strings.TrimSpace(config.LicenseStatus())))
				return
			}
			fmt.Println("License Type: INVALID / EXPIRED")
			fmt.Printf("License Key:  %s\n", licensing.MaskKey(key))
			fmt.Printf("Status:       %v\n", err)
			_ = config.SetLicense(key, "invalid", "")
			return
		}

		fmt.Println("License Type: PRO")
		fmt.Printf("License Key:  %s\n", licensing.MaskKey(key))
		fmt.Printf("Status:       %s (Verified with Polar.sh)\n", strings.ToUpper(result.Status))
		_ = config.SetLicense(key, result.Status, result.BenefitID)
	},
}

func init() {
	licenseCmd.AddCommand(licenseActivateCmd)
	licenseCmd.AddCommand(licenseStatusCmd)
	rootCmd.AddCommand(licenseCmd)
}
