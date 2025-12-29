package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v68/github"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

// Output
type OutputProject struct {
	URL         string   `yaml:"url"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Language    string   `yaml:"language"`
	License     string   `yaml:"license"`
	Date        string   `yaml:"date"`
	Topics      []string `yaml:"topics"`
	Stars       int      `yaml:"stars"`
	Forks       int      `yaml:"forks"`
	Issues      int      `yaml:"issues"`
	Icon        string   `yaml:"icon"`
	ForgeDomain string   `yaml:"forgeDomain"`
	ForgeEmoji  string   `yaml:"forgeEmoji"`
}

type OutputCategory struct {
	Title    string          `yaml:"title"`
	Projects []OutputProject `yaml:"projects"`
}

// Input
type InputCategory struct {
	Title    string         `yaml:"title"`
	Projects []InputProject `yaml:"projects"`
}

type InputProject struct {
	Repo string `yaml:"repo"`
	Icon string `yaml:"icon"`
}

// Forges
type ForgeConfig struct {
	Domain string `yaml:"domain"`
	Type   string `yaml:"type"` // github|forgejo
	API    string `yaml:"api"`
	CDN    string `yaml:"cdn"`
	Emoji  string `yaml:"emoji"`
}

type ForgeSecret struct {
	Domain string `yaml:"domain"`
	Token  string `yaml:"token"`
}

func main() {
	verbosity := flag.String("verbosity", "info", "Log level (debug, info, warn, error)")
	projectsFile := flag.String("projects", "projects.yaml", "Projects configuration file")
	forgesFile := flag.String("forges", "forges.yaml", "Forges configuration file")
	secretsFile := flag.String("secrets", "secrets.yaml", "Secrets configuration file")
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

	var forgesList []ForgeConfig
	if err := yaml.Unmarshal(forgesData, &forgesList); err != nil {
		panic(err)
	}

	forges := map[string]ForgeConfig{}
	for _, f := range forgesList {
		forges[f.Domain] = f
	}

	log.Info("Reading secrets configuration", "file", *secretsFile)

	secretsData, err := os.ReadFile(*secretsFile)
	if err != nil {
		panic(err)
	}

	var secretsList []ForgeSecret
	if err := yaml.Unmarshal(secretsData, &secretsList); err != nil {
		panic(err)
	}

	secrets := map[string]ForgeSecret{}
	for _, s := range secretsList {
		secrets[s.Domain] = s
	}

	clients := map[string]*github.Client{}
	for domain, forge := range forges {
		var httpClient *http.Client
		if secret, ok := secrets[domain]; ok && secret.Token != "" {
			httpClient = oauth2.NewClient(
				ctx,
				oauth2.StaticTokenSource(
					&oauth2.Token{
						AccessToken: secret.Token,
					},
				),
			)
		}

		client := github.NewClient(httpClient)
		client.BaseURL, err = url.Parse(forge.API)
		if err != nil {
			panic(err)
		}

		clients[domain] = client
		log.Debug("Initialized client for forge", "domain", domain, "api", forge.API)
	}

	log.Info("Reading projects file", "file", *projectsFile)

	input, err := os.ReadFile(*projectsFile)
	if err != nil {
		panic(err)
	}

	var parsedInput []InputCategory
	if err := yaml.Unmarshal(input, &parsedInput); err != nil {
		panic(err)
	}

	parsedOutput := []OutputCategory{}
	for _, inputCategory := range parsedInput {
		log.Info("Processing category", "title", inputCategory.Title)

		outputCategory := OutputCategory{
			Title:    inputCategory.Title,
			Projects: []OutputProject{},
		}

		for _, inputProject := range inputCategory.Projects {
			parts := strings.SplitN(inputProject.Repo, "/", 3)
			if len(parts) != 3 {
				panic(fmt.Errorf("invalid repo format, expected domain/owner/repo: %s", inputProject.Repo))
			}

			domain := parts[0]
			owner := parts[1]
			repo := parts[2]

			forge, ok := forges[domain]
			if !ok {
				panic(fmt.Errorf("unknown forge domain: %s", domain))
			}

			client, ok := clients[domain]
			if !ok {
				panic(fmt.Errorf("no client for forge domain: %s", domain))
			}

			log.Debug("Fetching repository", "domain", domain, "owner", owner, "repo", repo)

			project, _, err := client.Repositories.Get(ctx, owner, repo)
			if err != nil {
				panic(err)
			}

			license := ""
			if forge.Type == "github" { // Codeberg doesn't expose the project license via the API
				license = "UNLICENSED"
				if l := project.GetLicense(); l != nil {
					license = l.GetSPDXID()
				}
			}

			commits, _, err := client.Repositories.ListCommits(ctx, owner, repo, &github.CommitsListOptions{})
			if err != nil {
				panic(err)
			}

			latestCommitDate := project.GetPushedAt().Time
			if len(commits) > 0 {
				latestCommitDate = commits[0].Commit.Author.Date.Time
			}

			icon := ""
			if inputProject.Icon != "" {
				// GitHub and Forgejo/Codeberg have different CDN URL formats
				if forge.Type == "github" {
					icon = forge.CDN + owner + "/" + repo + "/" + project.GetDefaultBranch() + "/" + inputProject.Icon
				} else {
					icon = forge.CDN + owner + "/" + repo + "/raw/branch/" + project.GetDefaultBranch() + "/" + inputProject.Icon
				}
			}

			outputCategory.Projects = append(outputCategory.Projects, OutputProject{
				URL:         project.GetHTMLURL(),
				Title:       project.GetFullName(),
				Description: project.GetDescription(),
				Language:    project.GetLanguage(),
				License:     license,
				Date:        latestCommitDate.Format(time.RFC3339),
				Topics:      project.Topics,
				Stars:       project.GetStargazersCount(),
				Forks:       project.GetForksCount(),
				Issues:      project.GetOpenIssuesCount(),
				Icon:        icon,
				ForgeDomain: domain,
				ForgeEmoji:  forge.Emoji,
			})

			log.Debug("Processed repository", "fullName", project.GetFullName(), "stars", project.GetStargazersCount())
		}

		parsedOutput = append(parsedOutput, outputCategory)
	}

	markdownOutput := ""
	for _, outputCategory := range parsedOutput {
		sort.Slice(outputCategory.Projects, func(i, j int) bool {
			return outputCategory.Projects[i].Stars > outputCategory.Projects[j].Stars
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
						displayedTitle = path.Join(titleParts[0], titleParts[1])
					}
				}

				displayedTitle = project.ForgeEmoji + "/" + displayedTitle

				licensePart := ""
				if project.License != "" {
					licensePart = fmt.Sprintf(" ⚖️ %s", html.EscapeString(project.License))
				}

				projectMarkdown := fmt.Sprintf("<a display=\"inline\" target=\"_blank\" href=\"%s\"><b>%s%s</b></a> (⭐ %d 🛠️ %s%s 📅 %s) <br>%s",
					html.EscapeString(project.URL),
					iconMarkdown,
					html.EscapeString(displayedTitle),
					project.Stars,
					html.EscapeString(project.Language),
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
