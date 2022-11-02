make ran-simulator-docker
sudo docker save -o ~/Documents/onf_software/ran-simulator/ran-simulator.tar onosproject/ran-simulator:v0.10.6
sudo ctr -n k8s.io i import ~/Documents/onf_software/ran-simulator/ran-simulator.tar
kubectl scale deployment/ran-simulator -n riab --replicas=0
kubectl scale deployment/ran-simulator -n riab --replicas=1
