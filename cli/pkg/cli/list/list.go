package list

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"replicate.ai/cli/pkg/config"
	"replicate.ai/cli/pkg/console"
	"replicate.ai/cli/pkg/param"
	"replicate.ai/cli/pkg/project"
	"replicate.ai/cli/pkg/slices"
	"replicate.ai/cli/pkg/storage"
)

type Format int

const (
	FormatJSON = iota
	FormatTable
	FormatQuiet
)

const valueMaxLength = 20
const valueTruncate = 5

type Metric struct {
	Primary bool
	Name    string
	Value   float64
}

type ListExperiment struct {
	ID               string                  `json:"id"`
	Created          time.Time               `json:"created"`
	Params           map[string]*param.Value `json:"params"`
	Command          string                  `json:"command"`
	NumCheckpoints   int                     `json:"num_checkpoints"`
	LatestCheckpoint *project.Checkpoint     `json:"latest_checkpoint"`
	BestCheckpoint   *project.Checkpoint     `json:"best_checkpoint"`
	User             string                  `json:"user"`
	Host             string                  `json:"host"`
	Running          bool                    `json:"running"`

	// exclude config from json output
	Config *config.Config `json:"-"`
}

// TODO(andreas): make this safer and validate user inputs against these strings
func (exp *ListExperiment) GetValue(name string) *param.Value {
	if name == "started" {
		// floating point timestamp used in sorting
		return param.Float(float64(exp.Created.Unix()))
	}
	if name == "step" {
		if exp.LatestCheckpoint != nil {
			return param.Int(exp.LatestCheckpoint.Step)
		}
		return param.Int(0)
	}
	if name == "user" {
		return param.String(exp.User)
	}
	if name == "host" {
		return param.String(exp.Host)
	}
	if name == "command" {
		return param.String(exp.Command)
	}
	if name == "status" {
		if exp.Running {
			return param.String("running")
		}
		return param.String("stopped")
	}
	if exp.BestCheckpoint != nil {
		if val, ok := exp.BestCheckpoint.Metrics[name]; ok {
			return val
		}
	}
	if val, ok := exp.Params[name]; ok {
		return val
	}
	return nil
}

func Experiments(store storage.Storage, format Format, allParams bool, filters *param.Filters, sorter *param.Sorter) error {
	proj := project.NewProject(store)
	listExperiments, err := createListExperiments(proj, filters)
	if err != nil {
		return err
	}
	sort.Slice(listExperiments, func(i, j int) bool {
		return sorter.LessThan(listExperiments[i], listExperiments[j])
	})

	switch format {
	case FormatJSON:
		return outputJSON(listExperiments)
	case FormatTable:
		return outputTable(listExperiments, allParams)
	case FormatQuiet:
		return outputQuiet(listExperiments)
	}
	panic(fmt.Sprintf("Unknown format: %d", format))
}

func outputQuiet(experiments []*ListExperiment) error {
	for _, exp := range experiments {
		fmt.Println(exp.ID)
	}
	return nil
}

func outputJSON(experiments []*ListExperiment) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(experiments)
}

// output something like (TODO: this is getting very wide)
// experiment  started             status   host      user     param-1  latest   step  metric-1  best     step  metric-1
// 1eeeeee     10 seconds ago      running  10.1.1.1  andreas  100      3cccccc  20    0.02     2cccccc  20    0.01
// 2eeeeee     about a second ago  stopped  10.1.1.2  andreas  200      4cccccc  5              N/A
func outputTable(experiments []*ListExperiment, allParams bool) error {
	if len(experiments) == 0 {
		fmt.Println("No experiments found")
		return nil
	}

	paramsToDisplay := getParamsToDisplay(experiments, !allParams)
	metricsToDisplay := getMetricsToDisplay(experiments)

	// does any experiment have a primary metric?
	hasBestCheckpoint := false
	for _, exp := range experiments {
		if exp.BestCheckpoint != nil {
			hasBestCheckpoint = true
			break
		}
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	keys := []string{"EXPERIMENT", "STARTED", "STATUS", "HOST", "USER"}
	keys = append(keys, upper(paramsToDisplay)...)
	keys = append(keys, "LATEST CHECKPOINT")
	keys = append(keys, upper(metricsToDisplay)...)
	if hasBestCheckpoint {
		keys = append(keys, "BEST CHECKPOINT")
		keys = append(keys, upper(metricsToDisplay)...)
	}

	for i, key := range keys {
		fmt.Fprintf(tw, "%s", key)
		if i < len(keys)-1 {
			fmt.Fprint(tw, "\t")
		}
	}
	fmt.Fprint(tw, "\n")

	for _, exp := range experiments {
		// experiment
		fmt.Fprintf(tw, "%s\t", exp.ID[:7])

		// started
		fmt.Fprintf(tw, "%s\t", console.FormatTime(exp.Created))

		// status
		if exp.Running {
			fmt.Fprint(tw, "running\t")
		} else {
			fmt.Fprint(tw, "stopped\t")
		}

		// host
		fmt.Fprintf(tw, "%s\t", exp.Host)

		// user
		fmt.Fprintf(tw, "%s\t", exp.User)

		// experiment params
		for _, heading := range paramsToDisplay {
			if val, ok := exp.Params[heading]; ok {
				fmt.Fprint(tw, val.ShortString(valueMaxLength, valueTruncate))
			}
			fmt.Fprintf(tw, "\t")
		}

		latestCheckpoint := ""
		if exp.LatestCheckpoint != nil {
			latestCheckpoint = fmt.Sprintf("%s (step %s)", exp.LatestCheckpoint.ShortID(), strconv.Itoa(exp.LatestCheckpoint.Step))
		}
		fmt.Fprintf(tw, "%s\t", latestCheckpoint)

		// latest checkpoint metrics
		for _, heading := range metricsToDisplay {
			val := ""
			if exp.LatestCheckpoint != nil {
				if v, ok := exp.LatestCheckpoint.Metrics[heading]; ok {
					val = v.ShortString(valueMaxLength, valueTruncate)
				}
			}
			fmt.Fprintf(tw, "%s\t", val)
		}

		bestCheckpoint := ""

		if exp.BestCheckpoint != nil {
			bestCheckpoint = fmt.Sprintf("%s (step %s)", exp.BestCheckpoint.ShortID(), strconv.Itoa(exp.BestCheckpoint.Step))
		}
		fmt.Fprintf(tw, "%s\t", bestCheckpoint)

		// best checkpoint metrics
		for _, heading := range metricsToDisplay {
			val := ""
			if exp.BestCheckpoint != nil {
				if v, ok := exp.BestCheckpoint.Metrics[heading]; ok {
					val = v.ShortString(valueMaxLength, valueTruncate)
				}
			}
			fmt.Fprintf(tw, "%s\t", val)
		}

		// newline!
		fmt.Fprint(tw, "\n")
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	return nil
}

// Get experiment params to display in list. If onlyChangedParams is true, only return
// params which have changed across experiments.
func getParamsToDisplay(experiments []*ListExperiment, onlyChangedParams bool) []string {
	expHeadingSet := map[string]bool{}

	if onlyChangedParams {
		paramValues := map[string]*param.Value{}
		for _, exp := range experiments {
			for key, val := range exp.Params {
				// Don't show objects in list view, because they're likely long and not very helpful
				if val.Type() == param.TypeObject {
					continue
				}

				firstVal, ok := paramValues[key]
				if ok {
					notEqual, err := firstVal.NotEqual(val)
					if err != nil {
						console.Warn("%s", err)
					} else if notEqual {
						expHeadingSet[key] = true
					}
				} else {
					paramValues[key] = val
				}
			}
		}
	} else {
		for _, exp := range experiments {
			for key, val := range exp.Params {
				// Don't show objects in list view, because they're likely long and not very helpful
				if val.Type() == param.TypeObject {
					continue
				}
				expHeadingSet[key] = true
			}
		}
	}

	return slices.StringKeys(expHeadingSet)
}

// Get metrics to display for each checkpoint shown in list
func getMetricsToDisplay(experiments []*ListExperiment) []string {
	// TODO (bfirsh): make --all display all metrics from checkpoint

	metricsToDisplay := map[string]bool{}

	for _, exp := range experiments {
		if exp.BestCheckpoint == nil {
			continue
		}
		metricsToDisplay[exp.BestCheckpoint.PrimaryMetric.Name] = true
	}

	return slices.StringKeys(metricsToDisplay)
}

func createListExperiments(proj *project.Project, filters *param.Filters) ([]*ListExperiment, error) {
	experiments, err := proj.Experiments()
	if err != nil {
		return nil, err
	}
	ret := []*ListExperiment{}
	for _, exp := range experiments {
		listExperiment := &ListExperiment{
			ID:      exp.ID,
			Params:  exp.Params,
			Command: exp.Command,
			Created: exp.Created,
			Host:    exp.Host,
			User:    exp.User,
			Config:  exp.Config,
		}
		running, err := proj.ExperimentIsRunning(exp.ID)
		if err != nil {
			return nil, err
		}
		listExperiment.LatestCheckpoint = exp.LatestCheckpoint()
		listExperiment.BestCheckpoint = exp.BestCheckpoint()
		listExperiment.NumCheckpoints = len(exp.Checkpoints)
		listExperiment.Running = running

		match, err := filters.Matches(listExperiment)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		ret = append(ret, listExperiment)
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Created.Before(ret[j].Created)
	})

	return ret, nil

}

func upper(in []string) []string {
	ret := make([]string, len(in))
	for i, s := range in {
		ret[i] = strings.ToUpper(s)
	}
	return ret
}
