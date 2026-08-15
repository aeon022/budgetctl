package cmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var categoryCmd = &cobra.Command{
	Use:   "category",
	Short: "Manage categories",
}

var categoryRenameCmd = &cobra.Command{
	Use:   "rename <old> <new>",
	Short: "Rename a category everywhere — transactions, rules, and goals (merges into <new> if it already exists)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		n, err := s.RenameCategory(context.Background(), args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Renamed %q -> %q on %d transaction(s)\n", args[0], args[1], n)
		return nil
	},
}

var categoryTranslateCmd = &cobra.Command{
	Use:   "translate",
	Short: "AI-suggest renames for categories that don't match your categorization language (missionctl Bundle feature)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsPro() {
			fmt.Println("AI category translate is a missionctl Bundle feature.")
			fmt.Println("Get it at https://missionctl.sh/#pricing, then: budgetctl license activate <key>")
			return nil
		}
		apply, _ := cmd.Flags().GetBool("apply")
		lang, _ := cmd.Flags().GetString("lang")
		if lang == "" {
			lang = budget.CategoryLanguage()
		}

		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		categories, err := s.ListCategories(ctx)
		if err != nil {
			return err
		}
		if len(categories) == 0 {
			fmt.Println("No categories yet.")
			return nil
		}

		renames, err := budget.AITranslateCategories(ctx, categories, lang)
		if err != nil {
			return fmt.Errorf("AI translate: %w", err)
		}
		if len(renames) == 0 {
			fmt.Println("Nothing to rename — every category already fits.")
			return nil
		}

		olds := make([]string, 0, len(renames))
		for old := range renames {
			olds = append(olds, old)
		}
		sort.Strings(olds)

		for _, old := range olds {
			new := renames[old]
			if !apply {
				fmt.Printf("%s -> %s\n", old, new)
				continue
			}
			n, err := s.RenameCategory(ctx, old, new)
			if err != nil {
				return err
			}
			fmt.Printf("Renamed %q -> %q on %d transaction(s)\n", old, new, n)
		}
		if !apply {
			fmt.Printf("\n%d rename(s) proposed — re-run with --apply to make them.\n", len(renames))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(categoryCmd)
	categoryCmd.AddCommand(categoryRenameCmd)
	categoryCmd.AddCommand(categoryTranslateCmd)
	categoryTranslateCmd.Flags().Bool("apply", false, "Actually perform the renames instead of just previewing them")
	categoryTranslateCmd.Flags().String("lang", "", "Target language (ISO 639-1 code, default: detected from BUDGETCTL_CATEGORY_LANG/$LC_ALL/$LANG)")
}
