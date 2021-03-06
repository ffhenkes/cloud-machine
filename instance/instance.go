package instance

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"text/template"
	"time"

	"gopkg.in/amz.v3/ec2"
)

var loggerOutput io.Writer = os.Stderr
var logger = log.New(loggerOutput, "", 0)

func SetLogger(out io.Writer, prefix string, flag int) {
	loggerOutput = out
	logger = log.New(out, prefix, flag)
}

type Instance struct {
	Id                   string
	Name                 string
	Type                 string
	ImageId              string
	Region               string
	KeyName              string
	SecurityGroups       []string
	SubnetId             string
	DefaultAvailableZone string
	CloudConfig          string
	EBSOptimized         bool
	ShutdownBehavior     string
	EnableAPITermination bool
	PlacementGroupName   string
	ec2.Instance
}

func mergeInstances(instance *Instance, instanceRef *ec2.Instance) {
	instance.Instance = *instanceRef
	// Instance struct has some fields that is present in ec2.Instance
	// We should rewrite this fields
	instance.Id = instanceRef.InstanceId
	instance.Type = instanceRef.InstanceType
	instance.ImageId = instanceRef.ImageId
	instance.SubnetId = instanceRef.SubnetId
	instance.KeyName = instanceRef.KeyName
	instance.DefaultAvailableZone = instanceRef.AvailZone
	instance.EBSOptimized = instanceRef.EBSOptimized
	instance.SecurityGroups = make([]string, len(instanceRef.SecurityGroups))

	for i, securityGroup := range instanceRef.SecurityGroups {
		instance.SecurityGroups[i] = securityGroup.Id
	}

	for _, tag := range instanceRef.Tags {
		if tag.Key == "Name" {
			instance.Name = tag.Value
			break
		}
	}
}

/**
 * Valid values to state is: pending, running, shutting-down, terminated, stopping, stopped
 */
func WaitUntilState(ec2Ref *ec2.EC2, instance *Instance, state string) error {
	fmt.Fprintf(loggerOutput, "Instance state is <%s>, waiting for <%s>", instance.State.Name, state)
	for {
		fmt.Fprint(loggerOutput, ".")
		if instance.State.Name != state {
			time.Sleep(2 * time.Second)
			_, err := Load(ec2Ref, instance)
			if err != nil {
				fmt.Fprintln(loggerOutput, " [ERROR]")
				return err
			}
		} else {
			fmt.Fprintln(loggerOutput, " [OK]")
			return nil
		}
	}
}

/**
 * Get a instance, if Id was not passed a new instance will be created
 */
func Get(ec2Ref *ec2.EC2, instance *Instance) (ec2.Instance, error) {
	var instanceRef ec2.Instance
	var err error
	if instance.Id == "" {
		logger.Printf("Creating new instance...\n")
		instanceRef, err = Create(ec2Ref, instance)
		logger.Printf("--------- NEW INSTANCE ---------\n")
	} else {
		logger.Printf("Loading instance id <%s>...\n", instance.Id)
		instanceRef, err = Load(ec2Ref, instance)
		logger.Printf("--------- LOADING INSTANCE ---------\n")
	}

	if err != nil {
		return instanceRef, err
	}

	logger.Printf("    Id: %s\n", instance.Id)
	logger.Printf("    Name: %s\n", instance.Name)
	logger.Printf("    Type: %s\n", instance.Type)
	logger.Printf("    Image Id: %s\n", instance.ImageId)
	logger.Printf("    Available Zone: %s\n", instance.DefaultAvailableZone)
	logger.Printf("    Key Name: %s\n", instance.KeyName)
	logger.Printf("    Security Groups: %+v\n", instance.SecurityGroups)
	logger.Printf("    PlacementGroupName: %+v\n", instance.PlacementGroupName)
	logger.Printf("    Subnet Id: %s\n", instance.SubnetId)
	logger.Printf("    EBS Optimized: %t\n", instance.EBSOptimized)
	logger.Println("----------------------------------\n")

	return instanceRef, nil
}

/**
 * Load a instance passing its Id
 */
func Load(ec2Ref *ec2.EC2, instance *Instance) (ec2.Instance, error) {
	if instance.Id == "" {
		return ec2.Instance{}, errors.New("To load a instance you need to pass its Id")
	}

	resp, err := ec2Ref.Instances([]string{instance.Id}, nil)
	if err != nil {
		return ec2.Instance{}, err
	} else if len(resp.Reservations) == 0 || len(resp.Reservations[0].Instances) == 0 {
		return ec2.Instance{}, errors.New(fmt.Sprintf("Any instance was found with instance Id <%s>", instance.Id))
	}

	instanceRef := resp.Reservations[0].Instances[0]
	mergeInstances(instance, &instanceRef)

	return instanceRef, nil
}

/**
 * Create new instance
 */
func Create(ec2Ref *ec2.EC2, instance *Instance) (ec2.Instance, error) {
	options := ec2.RunInstances{
		ImageId:               instance.ImageId,
		InstanceType:          instance.Type,
		KeyName:               instance.KeyName,
		SecurityGroups:        make([]ec2.SecurityGroup, len(instance.SecurityGroups)),
		SubnetId:              instance.SubnetId,
		EBSOptimized:          instance.EBSOptimized,
		DisableAPITermination: !instance.EnableAPITermination,
	}

	if instance.CloudConfig != "" {
		cloudConfigTemplate, err := ioutil.ReadFile(instance.CloudConfig)
		if err != nil {
			panic(err.Error())
		}

		tpl := template.Must(template.New("cloudConfig").Parse(string(cloudConfigTemplate)))

		cloudConfig := new(bytes.Buffer)
		if err = tpl.Execute(cloudConfig, instance); err != nil {
			panic(err.Error())
		}

		options.UserData = cloudConfig.Bytes()
	}

	if instance.ShutdownBehavior != "" {
		options.ShutdownBehavior = instance.ShutdownBehavior
	}

	if instance.PlacementGroupName != "" {
		options.PlacementGroupName = instance.PlacementGroupName
	}

	for i, securityGroup := range instance.SecurityGroups {
		options.SecurityGroups[i] = ec2.SecurityGroup{Id: securityGroup}
	}

	resp, err := ec2Ref.RunInstances(&options)
	if err != nil {
		return ec2.Instance{}, err
	} else if len(resp.Instances) == 0 {
		return ec2.Instance{}, errors.New("Any instance was created!")
	}

	instanceRef := resp.Instances[0]
	_, err = ec2Ref.CreateTags([]string{instanceRef.InstanceId}, []ec2.Tag{{"Name", instance.Name}})
	if err != nil {
		return ec2.Instance{}, err
	}

	mergeInstances(instance, &instanceRef)

	err = WaitUntilState(ec2Ref, instance, "running")
	if err != nil {
		return ec2.Instance{}, err
	}

	return instanceRef, nil
}

func Terminate(ec2Ref *ec2.EC2, instance Instance) error {
	logger.Println("Terminating instance", instance.Id)
	_, err := ec2Ref.TerminateInstances([]string{instance.Id})
	if err == nil {
		logger.Printf("Instance <%s> was destroyed!\n", instance.Id)
	}

	return err
}

func Reboot(ec2Ref *ec2.EC2, instance Instance) error {
	logger.Println("Rebooting instance", instance.Id)
	_, err := ec2Ref.RebootInstances(instance.InstanceId)
	return err
}
