.. only:: not (epub or latex or html)

    WARNING: You are looking at unreleased Cilium documentation.
    Please use the official rendered version released here:
    https://docs.cilium.io

.. _testsuite:

Scalability and performance Testing
===================================

Introduction
~~~~~~~~~~~~

Cilium scalability tests leverage `Clusterloader2 <https://github.com/kubernetes/perf-tests/tree/master/clusterloader2>`_.
For overview of ClusterLoader2, please refer to the `official documentation <https://github.com/kubernetes/perf-tests/blob/master/clusterloader2/README.md>`_
and `getting started guide <https://github.com/kubernetes/perf-tests/blob/master/clusterloader2/docs/GETTING_STARTED.md>`_.

Running CL2 tests locally
^^^^^^^^^^^^^^^^^^^^^^^^^

Each CL2 test should be designed in a way that it scales with number of nodes. 
This allows for running specific test case scenario in a local enviroment, to validate the test case.
For example, let's try to run network policy scale test in kind cluster.
First, create a kind cluster as documented in `_dev_env`_.`
Build ClusterLoader2 binary from perf-tests repository.
Then you can run:

.. code-block:: bash

    export CL2_PROMETHEUS_PVC_ENABLED=false
    export CL2_PROMETHEUS_SCRAPE_CILIUM_OPERATOR=true
    export CL2_PROMETHEUS_SCRAPE_CILIUM_AGENT=true
    export CL2_PROMETHEUS_SCRAPE_CILIUM_AGENT_INTERVAL=5s

    ./clusterloader \
    -v=2 \
    --testconfig=.github/actions/cl2-modules/netpol/config.yaml \
    --provider=kind \
    --enable-prometheus-server \
    --tear-down-prometheus-server=false \
    --nodes=1 \
    --report-dir=./report \
    --experimental-prometheus-snapshot-to-report-dir=true \
    --prometheus-scrape-kube-proxy=false \
    --prometheus-apiserver-scrape-port=6443 \
    --enable-exec-service=false \
    --kubeconfig=$HOME/.kube/config



Then run the test:


Running tests in PR
^^^^^^^^^^^^^^^^^^^

Observability in perfdash
^^^^^^^^^^^^^^^^^^^^^^^^^

Resuts of the tests runs
^^^^^^^^^^^^^^^^^^^^^^^^