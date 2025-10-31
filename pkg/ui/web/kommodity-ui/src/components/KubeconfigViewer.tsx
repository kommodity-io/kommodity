import { useEffect, useState } from "react";
import { Download, Copy, Check } from "lucide-react";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { vscDarkPlus } from "react-syntax-highlighter/dist/esm/styles/prism";
import { toast } from "sonner";

export const KubeconfigViewer = ({ clusterName }: { clusterName: string }) => {
  const [kubeconfig, setKubeconfig] = useState<string>("");
  const [copied, setCopied] = useState(false);
  const [loading, setLoading] = useState(false);

  const getKommodityPort = () => {
    const raw = (import.meta as any)?.env?.VITE_KOMMODITY_PORT;
    const n = typeof raw === "string" ? parseInt(raw, 10) : NaN;
    return Number.isFinite(n) && n > 0 ? String(n) : "8000";
  };

  const buildApiUrl = (name: string) =>
    `http://localhost:${getKommodityPort()}/api/kubeconfig/${name}`;

  useEffect(() => {
    if (!clusterName) {
      setKubeconfig("");
      return;
    }

    const ac = new AbortController();
    const fetchKubeconfig = async () => {
      try {
        setLoading(true);
        const url = buildApiUrl(clusterName);
        const res = await fetch(url, { signal: ac.signal });

        if (!res.ok) {
          if (res.status === 404 || res.status === 500) {
            toast.error(`The cluster name: ${clusterName} is not a valid Kommodity cluster`);
          } else {
            toast.error(`Failed to fetch kubeconfig (HTTP ${res.status})`);
          }
          setKubeconfig("");
          return;
        }

        const text = await res.text();
        if (!text || !text.trim()) {
          toast.error(`The cluster name: ${clusterName} is not a valid Kommodity cluster`);
          setKubeconfig("");
          return;
        }

        setKubeconfig(text);
      } catch (err: any) {
        if (err?.name !== "AbortError") {
          toast.error("Failed to fetch kubeconfig. Please try again.");
          setKubeconfig("");
        }
      } finally {
        setLoading(false);
      }
    };

    fetchKubeconfig();
    return () => ac.abort();
  }, [clusterName]);

  const handleCopy = async () => {
    if (!kubeconfig.trim()) {
      toast.error("Nothing to copy yet—no kubeconfig loaded");
      return;
    }

    try {
      await navigator.clipboard.writeText(kubeconfig);
      setCopied(true);
      toast.success("Kubeconfig copied to clipboard");
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("Failed to copy. Please try again");
    }
  };

  const handleDownload = () => {
    if (!kubeconfig.trim()) {
      toast.error("Nothing to download yet—no kubeconfig loaded");
      return;
    }

    const blob = new Blob([kubeconfig], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${clusterName || "kubeconfig"}.yaml`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    toast.success("Kubeconfig file has been downloaded");
  };

  return (
    <div className="p-6 rounded-lg border border-border bg-card text-card-foreground shadow-sm animate-fade-in">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-xl font-semibold text-foreground">Your Kubeconfig</h2>
        <div className="flex gap-2">
          <button
            onClick={handleCopy}
            disabled={loading || !kubeconfig}
            className="inline-flex items-center justify-center gap-2 rounded-md text-sm font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 border border-input bg-background hover:bg-accent hover:text-accent-foreground h-9 px-3"
          >
            {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            {copied ? "Copied" : "Copy"}
          </button>
          <button
            onClick={handleDownload}
            disabled={loading || !kubeconfig}
            className="inline-flex items-center justify-center gap-2 rounded-md text-sm font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 bg-gradient-to-r from-primary to-primary-glow text-white hover:opacity-90 hover:shadow-glow h-9 px-3"
          >
            <Download className="h-4 w-4" />
            Download
          </button>
        </div>
      </div>

      <div className="code-block overflow-hidden">
        <SyntaxHighlighter
          language="yaml"
          style={vscDarkPlus}
          customStyle={{
            margin: 0,
            padding: "1.5rem",
            background: "hsl(var(--code-bg))",
            fontSize: "0.875rem",
            borderRadius: "0.5rem",
            minHeight: "6rem",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
          showLineNumbers
        >
          {loading
            ? "# Loading kubeconfig…"
            : kubeconfig || "# No kubeconfig loaded for this cluster."}
        </SyntaxHighlighter>
      </div>
    </div>
  );
};
