# Public ingress to administrative or data ports

An ingress rule from `0.0.0.0/0` or `::/0` on an administrative or database
port exposes that service to the whole internet. Authentication is not a
substitute for network scope on SSH, RDP, or a database listener.

## When to flag

- A security group, firewall, or network ACL ingress from `0.0.0.0/0`/`::/0`
  on an administrative or data port: 22 (SSH), 3389 (RDP), 5432
  (PostgreSQL), 3306 (MySQL/MariaDB), 1521 (Oracle), 1433 (SQL Server), 6379
  (Redis), 27017 (MongoDB), 9200/9300 (Elasticsearch), 11211 (memcached),
  2375/2376 (Docker), 6443/10250 (Kubernetes API/kubelet), 8500/8200
  (Consul/Vault).
- The same exposure through an `ipv6_cidr_blocks = ["::/0"]` rule, a
  `cidr_blocks` list that includes `0.0.0.0/0` beside a narrower entry, or a
  `source_security_group_id` replaced by a public CIDR.
- `publicly_accessible = true` on an RDS instance, a public load balancer in
  front of an admin service, or a public IP attached to a database host.
- A port range (`from_port = 0`, `to_port = 65535`) that includes the
  administrative ports above.
- A default security group with an ingress rule that opens an administrative
  port to a world CIDR; the group's default status is not a mitigation.

## Do NOT flag

- `0.0.0.0/0` on 80/443 (and 8080 behind an intended public load balancer),
  where the diff shows a public web service. That is the public surface, not
  an admin port.
- An ingress restricted to a source security group, a prefix list, a VPC
  CIDR, or a peered network.
- `0.0.0.0/0` on egress: outbound rules are normally broad and are not this
  finding.
- A public port protected by a documented allowlist at the application layer
  when the diff also shows the network scope is intentional (a public health
  endpoint).
- A port that is not in the administrative/data set and whose service is
  meant to be public; state the service before flagging.

## Evidence

Quote the ingress rule and the port. Name the service behind it and the
authentication or data an internet client can reach.
