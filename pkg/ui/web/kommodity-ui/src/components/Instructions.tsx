import { Terminal, FileText, CheckCircle2 } from "lucide-react";

const steps = [
  {
    icon: FileText,
    title: "Download the kubeconfig",
    description: "Click the download button above to save the kubeconfig file to your local machine.",
  },
  {
    icon: Terminal,
    title: "Set the KUBECONFIG environment variable",
    description: "Export the path to your kubeconfig file:",
    code: "export KUBECONFIG=/path/to/kubeconfig.yaml",
  },
  {
    icon: CheckCircle2,
    title: "Verify the connection",
    description: "Test your connection to the cluster:",
    code: "kubectl cluster-info\nkubectl get nodes",
  },
];

export const Instructions = () => {
  return (
    <div className="p-6 rounded-lg border border-border bg-card text-card-foreground shadow-sm animate-fade-in">
      <h2 className="text-xl font-semibold text-foreground mb-6">How to Apply</h2>
      
      <div className="space-y-6">
        {steps.map((step, index) => (
          <div key={index} className="flex gap-4">
            <div className="flex-shrink-0">
              <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center">
                <step.icon className="h-5 w-5 text-primary" />
              </div>
            </div>
            <div className="flex-1">
              <h3 className="font-semibold text-foreground mb-2">
                {index + 1}. {step.title}
              </h3>
              <p className="text-muted-foreground mb-3">{step.description}</p>
              {step.code && (
                <div className="code-block p-4 font-mono text-sm text-foreground">
                  <pre className="overflow-x-auto">{step.code}</pre>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>

      <div className="mt-8 p-4 rounded-lg bg-accent/10 border border-accent/20">
        <p className="text-sm text-muted-foreground">
          <span className="font-semibold text-accent">Note:</span> Make sure you have{" "}
          <code className="px-1.5 py-0.5 rounded bg-muted text-foreground text-xs">kubectl</code>{" "}
          installed on your system. Visit{" "}
          <a
            href="https://kubernetes.io/docs/tasks/tools/"
            target="_blank"
            rel="noopener noreferrer"
            className="text-primary hover:underline"
          >
            kubernetes.io/docs/tasks/tools
          </a>{" "}
          for installation instructions.
        </p>
      </div>
    </div>
  );
};
