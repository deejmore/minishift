/*
Copyright 2016 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	goflag "flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"encoding/json"

	"github.com/docker/machine/libmachine/log"
	"github.com/golang/glog"
	"github.com/minishift/minishift/cmd/minishift/cmd/addon"
	configCmd "github.com/minishift/minishift/cmd/minishift/cmd/config"
	"github.com/minishift/minishift/cmd/minishift/cmd/dns"
	hostfolderCmd "github.com/minishift/minishift/cmd/minishift/cmd/hostfolder"
	"github.com/minishift/minishift/cmd/minishift/cmd/image"
	cmdOpenshift "github.com/minishift/minishift/cmd/minishift/cmd/openshift"
	cmdProfile "github.com/minishift/minishift/cmd/minishift/cmd/profile"
	cmdUtil "github.com/minishift/minishift/cmd/minishift/cmd/util"
	"github.com/minishift/minishift/pkg/minikube/constants"
	minishiftConfig "github.com/minishift/minishift/pkg/minishift/config"
	minishiftConstants "github.com/minishift/minishift/pkg/minishift/constants"
	profileActions "github.com/minishift/minishift/pkg/minishift/profile"
	"github.com/minishift/minishift/pkg/util/filehelper"
	"github.com/minishift/minishift/pkg/util/os/atexit"
	minishiftStrings "github.com/minishift/minishift/pkg/util/strings"
	"github.com/minishift/minishift/pkg/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/minishift/minishift/cmd/minishift/state"
)

const (
	showLibmachineLogs    = "show-libmachine-logs"
	profileCmd            = "profile"
	profileFlag           = "profile"
	profileSetCmd         = "set"
	enableExperimentalEnv = "MINISHIFT_ENABLE_EXPERIMENTAL"
	invalidProfileName    = "Profile names must consist of alphanumeric characters only."
)

var noPersistentPreRunForCmds = []string{
	"version",
	"completion",
}
var viperWhiteList = []string{
	"v",
	"alsologtostderr",
	"log_dir",
}

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "minishift",
	Short: "Minishift is a tool for application development in local OpenShift clusters.",
	Long:  `Minishift is a command-line tool that provisions and manages single-node OpenShift clusters optimized for development workflows.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Return from here for commands included in 'noPersistentPreRunForCmds' and "minishift" as its parent.
		// This will result in no home dir when executing such commands.
		if minishiftStrings.Contains(noPersistentPreRunForCmds, cmd.Name()) && cmd.Parent().Name() == minishiftConstants.BinaryName {
			return
		}

		var (
			err                    error
			isAddonInstallRequired bool
		)

		// If profile name is 'minishift' then ignore the vaild profile check.
		if constants.ProfileName != constants.DefaultProfileName {
			checkForValidProfileOrExit(cmd)
		}

		constants.MachineName = constants.ProfileName
		constants.Minipath = constants.GetProfileHomeDir(constants.ProfileName)

		// Initialize the instance directory structure
		state.InstanceDirs = state.NewMinishiftDirs(constants.Minipath)

		constants.KubeConfigPath = filepath.Join(state.InstanceDirs.Machines, constants.MachineName+"_kubeconfig")

		if !filehelper.Exists(state.InstanceDirs.Addons) {
			isAddonInstallRequired = true
		}

		// creating all directories for minishift run
		createMinishiftDirs(state.InstanceDirs)

		// If AllInstanceConfig is not defined we should define it now.
		if minishiftConfig.AllInstancesConfig == nil {
			ensureAllInstanceConfigPath(constants.AllInstanceConfigPath)
			minishiftConfig.AllInstancesConfig, err = minishiftConfig.NewAllInstancesConfig(constants.AllInstanceConfigPath)
			if err != nil {
				atexit.ExitWithMessage(1, fmt.Sprintf("Error creating all instance config: %s", err.Error()))
			}
		}

		ensureConfigFileExists(constants.ConfigFile)

		// Create MACHINE_NAME.json
		instanceConfigPath := filepath.Join(constants.Minipath, "machines", constants.MachineName+".json")
		minishiftConfig.InstanceConfig, err = minishiftConfig.NewInstanceConfig(instanceConfigPath)
		if err != nil {
			atexit.ExitWithMessage(1, fmt.Sprintf("Error creating config for VM: %s", err.Error()))
		}

		if isAddonInstallRequired {
			if err := cmdUtil.UnpackAddons(state.InstanceDirs.Addons); err != nil {
				atexit.ExitWithMessage(1, fmt.Sprintf("Error installing default add-ons : %s", err))
			}
		}

		// Check marker file created by update command and perform post update execution steps
		if filehelper.Exists(filepath.Join(constants.Minipath, constants.UpdateMarkerFileName)) {
			if err := performPostUpdateExecution(filepath.Join(constants.Minipath, constants.UpdateMarkerFileName)); err != nil {
				atexit.ExitWithMessage(1, fmt.Sprintf("Error in performing post update exeuction: %s", err))
			}
		}

		if minishiftConfig.EnableExperimental {
			glog.Info("Experimental features are enabled")
		}

		shouldShowLibmachineLogs := viper.GetBool(showLibmachineLogs)
		if glog.V(3) {
			log.SetDebug(true)
		}
		if !shouldShowLibmachineLogs {
			log.SetOutWriter(ioutil.Discard)
			log.SetErrWriter(ioutil.Discard)
		}

		setDefaultActiveProfile()

		// Adding minishift version information to debug logs
		if glog.V(2) {
			fmt.Println(fmt.Sprintf("-- minishift version: v%s+%s", version.GetMinishiftVersion(), version.GetCommitSha()))
		}
	},
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		atexit.ExitWithMessage(1, err.Error())
	}
}

// Handle config values for flags used in external packages (e.g. glog)
// by setting them directly, using values from viper when not passed in as args
func setFlagsUsingViper() {
	for _, config := range viperWhiteList {
		var a = pflag.Lookup(config)
		viper.SetDefault(a.Name, a.DefValue)
		// If the flag is set, override viper value
		if a.Changed {
			viper.Set(a.Name, a.Value.String())
		}
		// Viper will give precedence first to calls to the Set command,
		// then to values from the config.yml
		a.Value.Set(viper.GetString(a.Name))
		a.Changed = true
	}
}

func processEnvVariables() {
	enableExperimental, err := cmdUtil.GetBoolEnv(enableExperimentalEnv)
	if err == cmdUtil.BooleanFormatError {
		atexit.ExitWithMessage(1, fmt.Sprintf("Error enabling experimental features: %s", err))
	}

	minishiftConfig.EnableExperimental = enableExperimental
}

func init() {
	processEnvVariables()
	RootCmd.PersistentFlags().Bool(showLibmachineLogs, false, "Show logs from libmachine.")
	RootCmd.PersistentFlags().String(profileFlag, constants.DefaultProfileName, "Profile name")
	RootCmd.AddCommand(configCmd.ConfigCmd)
	RootCmd.AddCommand(cmdOpenshift.OpenShiftCmd)
	RootCmd.AddCommand(hostfolderCmd.HostFolderCmd)
	RootCmd.AddCommand(addon.AddonsCmd)
	RootCmd.AddCommand(image.ImageCmd)
	RootCmd.AddCommand(cmdProfile.ProfileCmd)
	if minishiftConfig.EnableExperimental {
		RootCmd.AddCommand(dns.DnsCmd)
	}
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	logDir := pflag.Lookup("log_dir")
	if !logDir.Changed {
		logDir.Value.Set(constants.MakeMiniPath("logs"))
	}
	viper.BindPFlags(RootCmd.PersistentFlags())
	cobra.OnInitialize(initConfig)
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	profile := initializeProfile()
	if (profile != "") && (profile != constants.DefaultProfileName) {
		constants.ProfileName = profile
		constants.ConfigFile = constants.MakeMiniPath("profiles", profile, "config", "config.json")
	}
	configPath := constants.ConfigFile
	viper.SetConfigFile(configPath)
	viper.SetConfigType("json")
	err := viper.ReadInConfig()
	if err != nil {
		glog.Warningf("Error reading config file at '%s': %s", configPath, err)
	}
	setupViper()
}

// initializeProfile always return profile name based on below checks.
// 1. If profile set <PROFILE_NAME> is used then return PROFILE_NAME
// 2. If --profile <PROFILE_NAME> then return PROFILE_NAME
// 3. If no profile command or flag then return active profile name.
func initializeProfile() string {
	var (
		profileName     string
		err             error
		activeProfile   string
		profileCmdAlias = []string{
			"profiles",
			"instance",
		}
	)

	// Check if profileCmd is part of os.Args so that it takes preference instead `--profile` argument
	var isProfileCmdUsed bool
	for _, arg := range os.Args {
		if arg == profileCmd || arg == profileCmdAlias[0] || arg == profileCmdAlias[1] {
			isProfileCmdUsed = true
		}
	}

	for i, arg := range os.Args {
		if !isProfileCmdUsed {
			// This will match if `--profile` flag is used
			if arg == "--"+profileFlag {
				profileName = os.Args[i+1]
				break
			}
		}
		// This will match if we used profile or it's alias commands
		if arg == profileCmd || arg == profileCmdAlias[0] || arg == profileCmdAlias[1] {
			// This make sure if user specify profile command without any subcommand then
			// it should not panic with out of index error
			if len(os.Args) <= i+2 {
				break
			}
			// For use cases when minishift profile set PROFILE_NAME is used
			if os.Args[i+1] == profileSetCmd {
				profileName = os.Args[i+2]
			}
			break
		}
	}

	// Check if the allinstance config is present. If present we need to check active profile information.
	_, err = os.Stat(constants.AllInstanceConfigPath)
	if !os.IsNotExist(err) {
		minishiftConfig.AllInstancesConfig, err = minishiftConfig.NewAllInstancesConfig(constants.AllInstanceConfigPath)
		if err != nil {
			atexit.ExitWithMessage(1, fmt.Sprintf("Error initializing all instance config: %s", err.Error()))
		}
		activeProfile = profileActions.GetActiveProfile()
	}

	if profileName != "" {
		return profileName
	}
	if activeProfile != "" {
		return activeProfile
	}
	return ""
}

func setupViper() {
	viper.SetEnvPrefix(constants.MiniShiftEnvPrefix)
	// Replaces '-' in flags with '_' in env variables
	// e.g. show-libmachine-logs => $ENVPREFIX_SHOW_LIBMACHINE_LOGS
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
	setFlagsUsingViper()
}

func ensureConfigFileExists(configPath string) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		jsonRoot := []byte("{}")
		f, err := os.Create(configPath)
		if err != nil {
			glog.Exitf("Cannot create file '%s': %s", configPath, err)
		}
		defer f.Close()
		_, err = f.Write(jsonRoot)
		if err != nil {
			glog.Exitf("Cannot encode config '%s': %s", configPath, err)
		}
	}
}

// performPostUpdateExecution executes the post update actions like unpacking the default addons
// if user chose to update addons during `minishift update` command.
// It also remove the marker file created by update command to avoid repeating the post update execution process
func performPostUpdateExecution(markerPath string) error {
	var markerData UpdateMarker

	file, err := ioutil.ReadFile(markerPath)
	if err != nil {
		return err
	}

	json.Unmarshal(file, &markerData)
	if markerData.InstallAddon {
		fmt.Println(fmt.Sprintf("Minishift was upgraded from v%s to v%s. Running post update actions.", markerData.PreviousVersion, version.GetMinishiftVersion()))
		fmt.Print("--- Updating default add-ons ... ")
		cmdUtil.UnpackAddons(state.InstanceDirs.Addons)
		fmt.Println("OK")
		fmt.Println(fmt.Sprintf("Default add-ons '%s' installed", strings.Join(cmdUtil.DefaultAssets, ", ")))
	}

	// Delete the marker file once post update execution is done
	if err := os.Remove(markerPath); err != nil {
		return err
	}

	return nil
}

func ensureAllInstanceConfigPath(configPath string) {
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0777); err != nil {
		atexit.ExitWithMessage(1, fmt.Sprintf("Error creating directory: %s", configDir))
	}
}

func createMinishiftDirs(dirs *state.MinishiftDirs) {
	dirPaths := reflect.ValueOf(*dirs)

	for i := 0; i < dirPaths.NumField(); i++ {
		path := dirPaths.Field(i).Interface().(string)
		if err := os.MkdirAll(path, 0777); err != nil {
			atexit.ExitWithMessage(1, fmt.Sprintf("Error creating directory: %s", path))
		}
	}
}

// If there is no active profile we need to set minishift as the default profile.
// Because this will make the profile behaviour backward compatible and consistent with user expectation.
func setDefaultActiveProfile() {
	if minishiftConfig.AllInstancesConfig == nil {
		atexit.ExitWithMessage(1, "Error: All instance config is not initialized")
	}
	activeProfile := profileActions.GetActiveProfile()
	if activeProfile == "" {
		err := profileActions.SetDefaultProfileActive()
		if err != nil {
			atexit.ExitWithMessage(1, err.Error())
		}

		// Only set oc context to default profile when user is looking for default profile
		// i.e. "minishift start" with minishift as active profile or "minishift start --profile minishift"
		// Otherwise minishift will be the active profile irrespective of what user chooses
		if constants.ProfileName == constants.DefaultProfileName {
			cmdUtil.SetOcContext(constants.DefaultProfileName)
		}
	}
}

// checkForValidProfileOrExit checks if a profile exist or not when --profile flag used.
// If profile not exist then it will error out with message.
func checkForValidProfileOrExit(cmd *cobra.Command) {
	if !cmdUtil.IsValidProfileName(constants.ProfileName) {
		atexit.ExitWithMessage(1, invalidProfileName)
	}
	if cmd.Parent() != nil {
		// This condition true for each command execpt `minishift profile <subcommand>` and `minishift start ...``
		if cmd.Parent().Name() != profileCmd && cmd.Name() != startCmd.Name() {
			if !cmdUtil.IsValidProfile(constants.ProfileName) {
				atexit.ExitWithMessage(1, fmt.Sprintf("Profile '%s' doesn't exist, Use 'minishift profile set %s' or 'minishift start --profile %s' to create", constants.ProfileName, constants.ProfileName, constants.ProfileName))
			}
		}
	}
}
