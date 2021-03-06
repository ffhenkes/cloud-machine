package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/NeowayLabs/cloud-machine/machine"
	"github.com/NeowayLabs/cloud-machine/volume"
	"gopkg.in/amz.v3/aws"
	"gopkg.in/yaml.v2"
)

type (
	Clusters struct {
		Default  Default
		Clusters []struct {
			Machine string
			Nodes   int
		}
	}

	Cluster struct {
		Machine machine.Machine
		Nodes   int
	}

	Default struct {
		ImageId              string
		Region               string
		KeyName              string
		SecurityGroups       []string
		SubnetId             string
		DefaultAvailableZone string
	}
)

func main() {
	flag.Parse()

	clusterFile := flag.Arg(0)
	if clusterFile == "" {
		fmt.Printf("You need to pass the cluster file, type: %s <cluster-file.yml>\n", os.Args[0])
		return
	}

	clusterContent, err := ioutil.ReadFile(clusterFile)
	if err != nil {
		panic(err.Error())
	}

	var clusters Clusters
	err = yaml.Unmarshal(clusterContent, &clusters)
	if err != nil {
		panic(err.Error())
	}

	// First verify if I can open all machine files
	machines := make([]Cluster, len(clusters.Clusters))
	for key, _ := range clusters.Clusters {
		myCluster := &clusters.Clusters[key]

		machineContent, err := ioutil.ReadFile(myCluster.Machine)
		if err != nil {
			panic(err.Error())
		}

		var myMachine machine.Machine
		err = yaml.Unmarshal(machineContent, &myMachine)
		if err != nil {
			panic(err.Error())
		}

		// Verify if cloud-config file exists
		if myMachine.Instance.CloudConfig != "" {
			_, err := os.Stat(myMachine.Instance.CloudConfig)
			if err != nil {
				panic(err.Error())
			}
		}

		// Set default values of cluster to machine
		if myMachine.Instance.ImageId == "" {
			myMachine.Instance.ImageId = clusters.Default.ImageId
		}
		if myMachine.Instance.Region == "" {
			myMachine.Instance.Region = clusters.Default.Region
		}
		if myMachine.Instance.KeyName == "" {
			myMachine.Instance.KeyName = clusters.Default.KeyName
		}
		if len(myMachine.Instance.SecurityGroups) == 0 {
			myMachine.Instance.SecurityGroups = clusters.Default.SecurityGroups
		}
		if myMachine.Instance.SubnetId == "" {
			myMachine.Instance.SubnetId = clusters.Default.SubnetId
		}
		if myMachine.Instance.DefaultAvailableZone == "" {
			myMachine.Instance.DefaultAvailableZone = clusters.Default.DefaultAvailableZone
		}

		machines[key] = Cluster{Machine: myMachine, Nodes: myCluster.Nodes}
	}

	auth, err := aws.EnvAuth()
	if err != nil {
		panic(err.Error())
	}

	machine.SetLogger(ioutil.Discard, "", 0)

	for key, myCluster := range machines {
		fmt.Printf("================ Running machines of %d. cluster ================\n", key+1)

		for i := 1; i <= myCluster.Nodes; i++ {
			myMachine := myCluster.Machine
			myMachine.Volumes = make([]volume.Volume, len(myMachine.Volumes))

			// append machine number to name of instance
			myMachine.Instance.Name += fmt.Sprintf("-%d", i)

			// append machine number to name of volume
			for key, _ := range myCluster.Machine.Volumes {
				referenceVolume := &myCluster.Machine.Volumes[key]

				myVolume := *referenceVolume
				myVolume.Name += fmt.Sprintf("-%d", i)
				myMachine.Volumes[key] = myVolume
			}

			fmt.Printf("Running machine: %s\n", myMachine.Instance.Name)
			err = machine.Get(&myMachine, auth)
			if err != nil {
				panic(err.Error())
			}
			fmt.Printf("Machine id <%s>, ip address <%s>\n", myMachine.Instance.Id, myMachine.Instance.PrivateIPAddress)
			if i < myCluster.Nodes {
				fmt.Println("----------------------------------")
			}
		}
	}
	fmt.Println("================================================================")
}
