A group is one namespace of the cluster. Its unit carries the namespace's current objects, one line each
(Deployments, StatefulSets, DaemonSets, CronJobs, Services, Ingresses, PersistentVolumeClaims, ConfigMaps by
name), and for a change the lines that differ.

The targets are the pages that describe that namespace or the services it runs: an environment page, a service
page, a runbook that names the namespace. Choose the pages the plan's ownership table says own the namespace's
deployed state. Extract the images, replica counts, ports and exposed hosts; verify that each touched section
states what the unit's Observed block says and carries the observation date.
