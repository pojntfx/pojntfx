package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/pojntfx/felicitas.pojtinger.com/pkg/forges"
	"gopkg.in/yaml.v3"
)

func main() {
	verbosity := flag.String("verbosity", "info", "Log level (debug, info, warn, error)")
	projectsFile := flag.String("projects", "projects.yaml", "Projects configuration file")
	forgesFile := flag.String("forges", "forges.yaml", "Forges configuration file")
	tokens := flag.String("tokens", "", "Forge tokens as JSON object, e.g. {\"github.com\": \"token\"} (can also be set using the FORGE_TOKENS env variable)")
	user := flag.String("user", "pojntfx", "Default username to omit from display (can also be set using the FORGE_USER env variable)")

	flag.Parse()

	var level slog.Level
	if err := level.UnmarshalText([]byte(*verbosity)); err != nil {
		panic(err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))

	if userEnv := os.Getenv("FORGE_USER"); userEnv != "" {
		*user = userEnv
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("Reading forges configuration", "file", *forgesFile)

	forgesData, err := os.ReadFile(*forgesFile)
	if err != nil {
		panic(err)
	}

	var forgesList []forges.ForgeConfig
	if err := yaml.Unmarshal(forgesData, &forgesList); err != nil {
		panic(err)
	}

	if tokensEnv := os.Getenv("FORGE_TOKENS"); tokensEnv != "" {
		*tokens = tokensEnv
	}

	secrets := map[string]string{}
	if *tokens != "" {
		log.Info("Parsing forge tokens")

		if err := json.Unmarshal([]byte(*tokens), &secrets); err != nil {
			panic(fmt.Errorf("failed to parse tokens: %w", err))
		}
	}

	f, err := forges.OpenForges(ctx, forgesList, secrets)
	if err != nil {
		panic(err)
	}

	log.Debug("Initialized forge clients")

	log.Info("Reading projects file", "file", *projectsFile)

	input, err := os.ReadFile(*projectsFile)
	if err != nil {
		panic(err)
	}

	var inputCategories []forges.InputCategory
	if err := yaml.Unmarshal(input, &inputCategories); err != nil {
		panic(err)
	}

	log.Info("Fetching projects")

	outputCategories, err := f.FetchProjects(inputCategories)
	if err != nil {
		panic(err)
	}

	log.Info("Fetched all projects")

	markdownOutput := ""
	for _, outputCategory := range outputCategories {
		slices.SortFunc(outputCategory.Projects, func(a, b forges.OutputProject) int {
			return cmp.Compare(b.Stars, a.Stars)
		})

		markdownOutput += "\n| **" + html.EscapeString(outputCategory.Title) + "** | |\n| - | - |\n"

		for i := 0; i < len(outputCategory.Projects); {
			markdownLine := "| "
			for j := 0; j < 2 && i+j < len(outputCategory.Projects); j++ {
				project := outputCategory.Projects[i+j]

				parsedDate, err := time.Parse(time.RFC3339, project.Date)
				if err != nil {
					panic(err)
				}
				formattedDate := parsedDate.Format("2006")

				iconMarkdown := ""
				if project.Icon != "" {
					iconMarkdown = fmt.Sprintf("<img alt=\"Icon\" src=\"%s\" height=\"24\" align=\"top\"> ", html.EscapeString(project.Icon))
				}

				titleParts := strings.Split(project.Title, "/")
				displayedTitle := project.Title
				if len(titleParts) == 2 {
					if titleParts[0] == *user {
						displayedTitle = titleParts[1]
					} else {
						displayedTitle = strings.Join(titleParts[:2], "/")
					}
				}

				languagePart := ""
				if project.Language != "" {
					languagePart = fmt.Sprintf(", %s", html.EscapeString(project.Language))
				}

				licensePart := ""
				if project.License != "" {
					licensePart = fmt.Sprintf(", %s", html.EscapeString(project.License))
				}

				forgePrefix := ""
				if project.ForgeName != "" {
					forgePrefix = fmt.Sprintf("%s ", html.EscapeString(project.ForgeName))
				}

				projectMarkdown := fmt.Sprintf("<a display=\"inline\" target=\"_blank\" href=\"%s\"><b>%s%s</b></a> (%s⭐ %d%s%s, %s) <br>%s",
					html.EscapeString(project.URL),
					iconMarkdown,
					html.EscapeString(displayedTitle),
					forgePrefix,
					project.Stars,
					languagePart,
					licensePart,
					formattedDate,
					html.EscapeString(project.Description),
				)
				markdownLine += projectMarkdown + " | "
			}
			markdownOutput += markdownLine + "\n"
			i += 2
		}
	}

	fmt.Print(markdownOutput)
}
