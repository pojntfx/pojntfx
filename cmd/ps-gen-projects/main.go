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
	Slug string `yaml:"slug"`
	Icon string `yaml:"icon"`
}

func main() {
	verbosity := flag.String("verbosity", "info", "Log level (debug, info, warn, error)")
	src := flag.String("src", "projects.yaml", "Source YAML file")
	api := flag.String("api", "https://api.github.com/", "GitHub/Forgejo API endpoint to use (can also be set using the FORGE_API env variable)")
	cdn := flag.String("cdn", "https://raw.githubusercontent.com/", "GitHub/Forgejo CDN endpoint to use (can also be set using the FORGE_CDN env variable)")
	token := flag.String("token", "", "GitHub/Forgejo API access token (can also be set using the FORGE_TOKEN env variable)")
	user := flag.String("user", "pojntfx", "GitHub username (can also be set using the FORGE_USER env variable)")

	flag.Parse()

	var level slog.Level
	if err := level.UnmarshalText([]byte(*verbosity)); err != nil {
		panic(err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))

	if apiEnv := os.Getenv("FORGE_API"); apiEnv != "" {
		*api = apiEnv
	}

	if cdnEnv := os.Getenv("FORGE_CDN"); cdnEnv != "" {
		*cdn = cdnEnv
	}

	if tokenEnv := os.Getenv("FORGE_TOKEN"); tokenEnv != "" {
		*token = tokenEnv
	}

	if userEnv := os.Getenv("FORGE_USER"); userEnv != "" {
		*user = userEnv
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("Reading input file", "src", *src)

	input, err := os.ReadFile(*src)
	if err != nil {
		panic(err)
	}

	var parsedInput []InputCategory
	if err := yaml.Unmarshal(input, &parsedInput); err != nil {
		panic(err)
	}

	var httpClient *http.Client
	if *token != "" {
		httpClient = oauth2.NewClient(
			ctx,
			oauth2.StaticTokenSource(
				&oauth2.Token{
					AccessToken: *token,
				},
			),
		)
	}

	client := github.NewClient(httpClient)
	client.BaseURL, err = url.Parse(*api)
	if err != nil {
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
			owner, repo := path.Split(inputProject.Slug)
			ownerTrimmed := strings.TrimSuffix(owner, "/")

			log.Debug("Fetching repository", "owner", ownerTrimmed, "repo", repo)

			project, _, err := client.Repositories.Get(ctx, ownerTrimmed, repo)
			if err != nil {
				panic(err)
			}

			license := "UNLICENSED"
			if l := project.GetLicense(); l != nil {
				license = l.GetSPDXID()
			}

			commits, _, err := client.Repositories.ListCommits(ctx, ownerTrimmed, repo, &github.CommitsListOptions{})
			if err != nil {
				panic(err)
			}

			latestCommitDate := project.GetPushedAt().Time
			if len(commits) > 0 {
				latestCommitDate = commits[0].Commit.Author.Date.Time
			}

			icon := ""
			if inputProject.Icon != "" {
				icon = *cdn + owner + repo + "/" + project.GetDefaultBranch() + "/" + inputProject.Icon
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

				displayedTitle := project.Title
				if strings.Split(project.Title, "/")[0] == *user {
					displayedTitle = strings.Split(project.Title, "/")[1] // Use just the repo name if owner matches the GitHub user
				}

				projectMarkdown := fmt.Sprintf("<a display=\"inline\" target=\"_blank\" href=\"%s\"><b>%s%s</b></a> (⭐ %d 🛠️ %s ⚖️ %s 📅 %s) <br>%s",
					html.EscapeString(project.URL),
					iconMarkdown,
					html.EscapeString(displayedTitle),
					project.Stars,
					html.EscapeString(project.Language),
					html.EscapeString(project.License),
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
