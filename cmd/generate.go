package cmd

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/Azure/acs-engine/pkg/acsengine"
	"github.com/Azure/acs-engine/pkg/acsengine/transform"
	"github.com/Azure/acs-engine/pkg/api"
	"github.com/Azure/acs-engine/pkg/i18n"
	"github.com/leonelquinteros/gotext"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	generateName             = "generate"
	generateShortDescription = "Generate an Azure Resource Manager template"
	generateLongDescription  = "Generates an Azure Resource Manager template, parameters file and other assets for a cluster"
)

type generateCmd struct {
	apimodelPath      string
	outputDirectory   string // can be auto-determined from clusterDefinition
	caCertificatePath string
	caPrivateKeyPath  string
	classicMode       bool
	noPrettyPrint     bool
	parametersOnly    bool

	// derived
	containerService *api.ContainerService
	apiVersion       string
	locale           *gotext.Locale
}

func newGenerateCmd() *cobra.Command {
	gc := generateCmd{}

	generateCmd := &cobra.Command{
		Use:   generateName,
		Short: generateShortDescription,
		Long:  generateLongDescription,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := gc.validate(cmd, args); err != nil {
				log.Fatalf(fmt.Sprintf("error validating generateCmd: %s", err.Error()))
			}
			return gc.run()
		},
	}

	f := generateCmd.Flags()
	f.StringVar(&gc.apimodelPath, "api-model", "", "")
	f.StringVar(&gc.outputDirectory, "output-directory", "", "output directory (derived from FQDN if absent)")
	f.StringVar(&gc.caCertificatePath, "ca-certificate-path", "", "path to the CA certificate to use for Kubernetes PKI assets")
	f.StringVar(&gc.caPrivateKeyPath, "ca-private-key-path", "", "path to the CA private key to use for Kubernetes PKI assets")
	f.BoolVar(&gc.classicMode, "classic-mode", false, "enable classic parameters and outputs")
	f.BoolVar(&gc.noPrettyPrint, "no-pretty-print", false, "skip pretty printing the output")
	f.BoolVar(&gc.parametersOnly, "parameters-only", false, "only output parameters files")

	return generateCmd
}

func (gc *generateCmd) validate(cmd *cobra.Command, args []string) error {
	var caCertificateBytes []byte
	var caKeyBytes []byte
	var err error

	gc.locale, err = i18n.LoadTranslations()
	if err != nil {
		return fmt.Errorf(fmt.Sprintf("error loading translation files: %s", err.Error()))
	}

	if gc.apimodelPath == "" {
		if len(args) == 1 {
			gc.apimodelPath = args[0]
		} else if len(args) > 1 {
			cmd.Usage()
			return errors.New("too many arguments were provided to 'generate'")
		} else {
			cmd.Usage()
			return errors.New("--api-model was not supplied, nor was one specified as a positional argument")
		}
	}

	if _, err := os.Stat(gc.apimodelPath); os.IsNotExist(err) {
		return fmt.Errorf(fmt.Sprintf("specified api model does not exist (%s)", gc.apimodelPath))
	}

	apiloader := &api.Apiloader{
		Translator: &i18n.Translator{
			Locale: gc.locale,
		},
	}
	gc.containerService, gc.apiVersion, err = apiloader.LoadContainerServiceFromFile(gc.apimodelPath, true, false, nil)
	if err != nil {
		return fmt.Errorf(fmt.Sprintf("error parsing the api model: %s", err.Error()))
	}

	if gc.outputDirectory == "" {
		if gc.containerService.Properties.MasterProfile != nil {
			gc.outputDirectory = path.Join("_output", gc.containerService.Properties.MasterProfile.DNSPrefix)
		} else {
			gc.outputDirectory = path.Join("_output", gc.containerService.Properties.HostedMasterProfile.DNSPrefix)
		}
	}

	// consume gc.caCertificatePath and gc.caPrivateKeyPath

	if (gc.caCertificatePath != "" && gc.caPrivateKeyPath == "") || (gc.caCertificatePath == "" && gc.caPrivateKeyPath != "") {
		return errors.New("--ca-certificate-path and --ca-private-key-path must be specified together")
	}
	if gc.caCertificatePath != "" {
		if caCertificateBytes, err = ioutil.ReadFile(gc.caCertificatePath); err != nil {
			return fmt.Errorf(fmt.Sprintf("failed to read CA certificate file: %s", err.Error()))
		}
		if caKeyBytes, err = ioutil.ReadFile(gc.caPrivateKeyPath); err != nil {
			return fmt.Errorf(fmt.Sprintf("failed to read CA private key file: %s", err.Error()))
		}

		prop := gc.containerService.Properties
		if prop.CertificateProfile == nil {
			prop.CertificateProfile = &api.CertificateProfile{}
		}
		prop.CertificateProfile.CaCertificate = string(caCertificateBytes)
		prop.CertificateProfile.CaPrivateKey = string(caKeyBytes)
	}
	return nil
}

func (gc *generateCmd) run() error {
	log.Infoln(fmt.Sprintf("Generating assets into %s...", gc.outputDirectory))

	ctx := acsengine.Context{
		Translator: &i18n.Translator{
			Locale: gc.locale,
		},
	}
	templateGenerator, err := acsengine.InitializeTemplateGenerator(ctx, gc.classicMode)
	if err != nil {
		log.Fatalln("failed to initialize template generator: %s", err.Error())
	}

	template, parameters, certsGenerated, err := templateGenerator.GenerateTemplate(gc.containerService, acsengine.DefaultGeneratorCode, false, BuildTag)
	if err != nil {
		log.Fatalf("error generating template %s: %s", gc.apimodelPath, err.Error())
		os.Exit(1)
	}

	if !gc.noPrettyPrint {
		if template, err = transform.PrettyPrintArmTemplate(template); err != nil {
			log.Fatalf("error pretty printing template: %s \n", err.Error())
		}
		if parameters, err = transform.BuildAzureParametersFile(parameters); err != nil {
			log.Fatalf("error pretty printing template parameters: %s \n", err.Error())
		}
	}

	writer := &acsengine.ArtifactWriter{
		Translator: &i18n.Translator{
			Locale: gc.locale,
		},
	}
	if err = writer.WriteTLSArtifacts(gc.containerService, gc.apiVersion, template, parameters, gc.outputDirectory, certsGenerated, gc.parametersOnly); err != nil {
		log.Fatalf("error writing artifacts: %s \n", err.Error())
	}

	return nil
}
