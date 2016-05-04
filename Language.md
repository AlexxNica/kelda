Policy Language Design
======================
Designing languages is hard, so it's a good idea to get started early.
This is a living proposal for the policy language.  It may or may not reflect
the actual implementation at any given point in time.

## Syntax
Currently, the policy language is a simplified Lisp.  Lisp is a good choice at
this early stage because its simplicity will allow rapid development and
experimentation.  Eventually, once the design of the language solidifies, we
will implement a more modern syntax for the language.  The Lisp will still
stick around as an intermediate language, but users won't have to deal with it
directly.

The language will cover the basic things we expect from a lisp including
arithmetic, variable binding, conditionals, etc.  We explicitly will not
support recursion thus guaranteeing that all specifications terminate.

## Atoms
```
(docker <image>)
```
Atoms represent the basic unit of computation instantiated by the policy
language.  Typically these will be containers (or kubernetes pods perhaps), but
they may also represent external services (hostnames/IPs).  They are
instantiated in the policy language by simply declaring them (along with their
basic configuration).

```
(docker ubuntu:15.10) # Boot an Ubuntu 15.10 container.
(makeList 5 (docker ubuntu)) # Boot 5 ubuntu:latest containers.
(host external.org) # Register external.org as a reachable hostname.
```

Atoms can also be used to describe administrative constructs that aren't
directly implemented in the dataplane.  Administrators for example:

```
(user github ejj) # Github user ejj
(user github melvinw) # Github user melvinw
```

As DI supports more functionality, atoms will naturally expand to implement
more concepts.

### SSH Keys
SSH keys are represented as atoms. Specifically, there's `(plaintextKey <key>)` and
`(githubKey <username>)`.

#### Machines
Each instance of a machine is also an atom. A machine is defined as
`(machine <attributes>)` where `<attributes>` are either `(provider <provider>)`,
`(region <region>)`, `(diskSize <diskSize>)`, or `(size <size>)`. For example,
```
(machine (provider "Amazon") (size "m4.large") (region "us-west-2") (diskSize 32))
```

The attributes of labeled machines can be later modified with
`(machineAttribute <machine> <attributes>)`. For example,
```
(label "masters" (makeList 2 (machine)))
(machineAttribute "masters" (size "m3.medium") (provider "Amazon"))
```
If the attribute is already defined, it is replaced. `machineAttributes` works
both for a list of machines and a single instance.

The `attributes` for both `machine` and `machineAttribute` are both flattened
before being applied. This allows you to do `define` settings as lists and then
apply them.
```
(define large (list (ram 16) (cpu 8)))
(label "machines" (machine (provider "Amazon") large))
```

##### Ranges
Some attributes (`ram` and `cpu`) can be defined as ranges. Ranges are converted
by the engine into a provider-specific instance size. The `size` attribute
has precedence over ranges.
```
(define MaxPrice 1)
(machine (ram 4 8))
(machine (ram 4))
```
A range can be one or two values:  if it's two, then the range represents a min
and max, and if it's one, then it represents just a min.

If `MaxPrice` is defined, then a size is only selected based on the range if the
selection for a single machine is less than `MaxPrice`.

## Functions
There are two types of functions: built-ins, and `lambda` functions.

#### Built-ins
Built-ins have special evaluation functions written in `go`. Examples include
`placement` and `connect`.

#### Lambda
`lambda` functions are written in the spec language, and can be defined by the
user. Lambda functions must be declared in the following form:

`(lambda (<arg_names>) (<body>))`

For example, a `lambda` for adding 2 to a number could be written as:

`(lambda (x) (+ x 2))`

Lambdas can be given names by combining them with a `define`:

`(define Add2 (lambda (x) (+ x 2)))`

#### Evaluation
Both built-ins and lambda functions are evaluted in the same way. The first item in the S-expression
refers to the function to be invoked, and the remaining items are the arguments.

```
(+ 2 2) // => 4

((lambda (x) (+ x 2)) 2) // => 4

(define Add2 (lambda (x) (+ x 2)))
(Add2 2) // => 4

(let ((Add2 (lambda (x) (+ x 2))))
    (Add2 2)
) // => 4
```

## Modules
`module` is a way of creating a namespace. It evalutes its body, and then makes
exportable binds and labels available as `<module_name>.ident`.
Only binds and labels that start with a capital letter are exported.
```
(module "math" (define Square (lambda (x) (* x x))))
(math.Square 5) // => 25
```

`import` is a way of importing code in other files. It evaluates to a `module`
where the module name is the filename (minus the .spec extension), and the module
body is the contents of the body.

```
// math.spec
(define Square (lambda (x) (* x x)))


// main.spec
(import "math")
(math.Square 5) // => 25
```

### DI_PATH
`di` looks for imports according to the `DI_PATH` environment variable.
So if you have a spec that imports `stdlib`, and `stdlib.spec` is located at
`specs/stdlib.spec`, then your `DI_PATH` should be `DI_PATH="specs"`.

Multiple paths are separated by colons, so you may do `DI_PATH="specs:specs/spark"`.

You can invoke `di` with the path in one line:
```
DI_PATH="specs" ./di -c config.spec
```

or `export` it into your environment.

## Labels
```
(label <name> <member list>)
```
Each atom has associated with it a collection of labels that will be used in
the application data plane.  Labels apply to a set of one or more atoms and
labels -- essentially they're named sets.  Recursion is not allowed.  Labels
may not label themselves.

```
# A database is a postgres container.
(label database (docker postgres))

# These 5 apache containers make up the webTier.
(label webTier (makeList 5 (docker apache))

# A deployment consists of a database, a webTier, and a monitoring system
(label deployment (list database webTier (docker monitor)))
```

The same labelling construction will be used for authentication policy as well.

```
# ejj is a graduate student.
(label grad (user github ejj))

# melvinw is an undergraduate
(label undergrad (user github melvinw)

# Undergraduate and graduate students are admins.
(label admin (list grad undergrad))
```

As DI supports more use cases, the same labeling system will apply naturally.

##### Open Questions
* How do overlapping labels work?  Seems like labels should be lexically scoped
  somehow, but it's not clear how the syntax would work.  Perhaps it should
  work more like the **let** keyword?

## Connect
```
(connect <port> <from> <to>)
```
**connect** explicitly allows communication  between the atoms implementing two
labels.  Atoms implementing the *from* label may initiate network connections
to atoms implementing the *to* label over the specified network *port*.
```
# Allow the public internet to connect to the webTier over port 80
(connect 80 public webTier)

# Allow the webTier to connect to the database on port 1433
(connect 1433 webTier database)

# Allow members of the database tier to talk to each other over any port
(connect (list 0 65535) database database)
```
##### Service Discovery
The labels used in the **connect** keyword have real meaning in the
application dataplane.  The *to* label will be made available to the *from*
atoms as a hostname on which sockets can be opened.  In the above
example the containers implementing *webTier* may open a socket to the
hostname "database.di" which will connect them to the appropriate database
containers.

##### Load Balancing
The **connect** keyword automatically detects if the *to* label consists of a
single or multiple atoms.  If it's a single atom, it allows direct connections.
However, for connections to multiple atoms, the dataplane will automatically
load balance new connections across all available atoms.

##### Firewalling
By default, atoms in DI cannot communicate with each other due to an implicit
"deny all" firewall.  Communication between atoms must be explicitly permitted
by the **connect** keyword.

## Placement
```
(placement <PLACEMENT_TYPE> <label1> <label2> ... <labelN>)
```

Placement Types:
- `exclusive`: Any instance labeled `label1` will never be placed on the
same host as an instance labeled `label2` and vice-versa. If constraints
can't be satisfied then they won't be scheduled.

```
// A 'webServer' and 'database' will never share a host
(label webServer (docker apache))
(label database (docker mysql))
(placement "exclusive" "webServer" "database")

// A 'dataPipeline' will never share a host with another 'dataPipeline'
(label dataPipeline (docker spark))
(placement "exclusive" "dataPipeline" "dataPipeline")
```

