// Copyright 2018 Deep Impact AG. All rights reserved.
// Use of this source code is governed by the Apache License Version 2.0
// that can be found in the LICENSE file.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/mitchellh/go-homedir"
	"gopkg.in/ini.v1"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	version = "dev"
	verbose *bool
)

const admDuration = 3600
const standardDuration = 36000

// Limits that AWS enforces on sts:GetSessionToken.
const minSessionDuration = 15 * time.Minute
const maxSessionDuration = 36 * time.Hour

func main() {
	profile, region, durationOverride := parseArguments()
	vPrintln("Loading the AWS profile %s", profile)
	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithSharedConfigProfile(profile),
	)
	if err != nil {
		panic("unable to load SDK config, " + err.Error())
	}
	if len(region) == 0 {
		region = cfg.Region
	}
	_, err = cfg.Credentials.Retrieve(context.TODO())
	if err != nil {
		panic("unable to retrieve credentials from profile")
	}
	vPrintln("Using the AWS Region %s", region)
	iamc := iam.NewFromConfig(cfg)
	stsc := sts.NewFromConfig(cfg)
	arnChan := mfaDeviceArnChan(stsc, iamc)
	tokenCode := tokenCode()
	mfaDeviceArn := pullMfaDeviceArn(arnChan)
	credentials := sessionCredentials(stsc, mfaDeviceArn, tokenCode, duration(profile, durationOverride))
	storeCredentials(profile, region, credentials)
	storeConfig(profile, region)
	fmt.Printf("Credentials valid until: %s\n", credentials.Expiration)
}

func parseArguments() (string, string, time.Duration) {
	flag.Usage = printUsage
	region := flag.String("r", "", "Override the AWS Region")
	dur := flag.Duration("d", 0, "Override the session duration, e.g. 8h or 90m (15m to 36h; default 10h, 1h for -adm profiles)")
	version := flag.Bool("V", false, "Print the skuld version")
	verbose = flag.Bool("v", false, "Print verbose log")
	help := flag.Bool("h", false, "Print this help message")
	flag.Parse()
	tail := flag.Args()
	if *help {
		printUsage()
		os.Exit(0)
	}
	if *version {
		printVersion()
		os.Exit(0)
	}
	if len(tail) != 1 {
		printUsage()
		os.Exit(2)
	}
	if *dur != 0 && (*dur < minSessionDuration || *dur > maxSessionDuration) {
		fmt.Printf("The duration must be between %s and %s, got %s.\n", minSessionDuration, maxSessionDuration, *dur)
		os.Exit(2)
	}
	return tail[0], *region, *dur
}

func printUsage() {
	println("skuld [-r region] [-d duration] <aws profile>")
	printVersion()
	flag.PrintDefaults()
}

func printVersion() {
	fmt.Printf("Skuld version: %s\n", version)
}

type arn struct {
	arn   string
	error interface{}
}

func mfaDeviceArnChan(stsc *sts.Client, iamc *iam.Client) chan arn {
	result := make(chan arn)
	go func() {
		defer func() {
			if err := recover(); err != nil {
				result <- arn{error: err}
			}
		}()
		deviceArn := mfaDeviceArn(stsc, iamc)
		result <- arn{arn: deviceArn}
	}()
	return result
}

func mfaDeviceArn(stsc *sts.Client, iamc *iam.Client) string {
	userArn := userArn(stsc)
	vPrintln("Fetching MFA Device Arn")
	mfaDevice, err := iamc.ListMFADevices(
		context.TODO(),
		&iam.ListMFADevicesInput{UserName: &userArn},
	)
	if err != nil {
		println(err.Error())
		panic("Unable to fetch the MFA device Arn.")
	}
	return *mfaDevice.MFADevices[0].SerialNumber
}

func userArn(stsc *sts.Client) string {
	vPrintln("Fetching Client User Arn")
	callerIdResp, err := stsc.GetCallerIdentity(
		context.TODO(),
		nil,
	)
	if err != nil {
		panic("Unable to get the userArn.")
	}
	arn := callerIdResp.Arn
	return strings.Split(*arn, ":user/")[1]
}

func pullMfaDeviceArn(arn chan arn) string {
	mfaDeviceArn := <-arn
	if mfaDeviceArn.error != nil {
		panic(mfaDeviceArn.error)
	}
	return mfaDeviceArn.arn
}

func duration(profile string, override time.Duration) int32 {
	if override > 0 {
		return int32(override.Seconds())
	}
	if strings.HasSuffix(profile, "-adm") {
		return admDuration
	}
	return standardDuration
}

func tokenCode() string {
	var tokenCode string
	fmt.Print("Enter your token: ")
	_, err := fmt.Scanf("%s", &tokenCode)
	if err != nil {
		panic(err)
	}
	fmt.Println("Fetching temporary credentials...")
	return tokenCode
}

func sessionCredentials(stsc *sts.Client, mfaDevice string, tokenCode string, duration int32) *types.Credentials {
	token, err := stsc.GetSessionToken(
		context.TODO(),
		&sts.GetSessionTokenInput{
			SerialNumber:    &mfaDevice,
			DurationSeconds: &duration,
			TokenCode:       &tokenCode,
		},
	)
	if err != nil {
		println(err.Error())
		panic("Unable to create a new session.")
	}
	return token.Credentials
}

func storeCredentials(profile string, region string, credentials *types.Credentials) {
	credsFile := awsFile("credentials")
	creds, err := ini.Load(credsFile)
	if err != nil {
		panic("Unable to load the credential file.")
	}
	tokenProfile := skuldProfile(profile)
	section := creds.Section(tokenProfile)
	section.Key("aws_access_key_id").SetValue(*credentials.AccessKeyId)
	section.Key("aws_secret_access_key").SetValue(*credentials.SecretAccessKey)
	section.Key("aws_session_token").SetValue(*credentials.SessionToken)
	section.Key("region").SetValue(region)
	err = creds.SaveTo(credsFile)
	if err != nil {
		panic("Could not save the credential file.")
	}
}

func awsFile(fileName string) string {
	dir, err := homedir.Dir()
	if err != nil {
		panic(err.Error())
	}
	return filepath.Join(dir, ".aws", fileName)
}

func skuldProfile(profile string) string {
	return profile + "-skuld"
}

func storeConfig(profile string, region string) {
	configFile := awsFile("config")
	config, err := ini.Load(configFile)
	if err != nil {
		panic("Unable to load the config file.")
	}
	tokenProfile := skuldProfile(profile)
	section := config.Section(tokenProfile)
	section.Key("region").SetValue(region)
	err = config.SaveTo(configFile)
	if err != nil {
		panic("Unable to save the config file")
	}
}

func vPrintln(format string, a ...interface{}) {
	if *verbose {
		fmt.Printf(format, a...)
		fmt.Println("")
	}
}
