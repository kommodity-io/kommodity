import { KubeconfigViewer } from "@/components/KubeconfigViewer";
import { Instructions } from "@/components/Instructions";
import { useParams } from "react-router-dom";

const Index = () => {
  const { clusterName } = useParams();

  return (
    <div className="min-h-screen bg-background">
      {/* Main Content */}
      <main className="container mx-auto px-4 py-16">
        <div className="max-w-5xl mx-auto space-y-8">
          {/* Hero Section */}
          <div className="text-center space-y-4 mb-12 animate-fade-in">
            <h2 className="text-4xl font-bold text-foreground">
              Your Cluster Configuration
            </h2>
            <p className="text-lg text-muted-foreground max-w-2xl mx-auto">
              Download or copy your kubeconfig file and follow the instructions below to connect to your Kubernetes cluster.
            </p>
          </div>

          {/* Kubeconfig Viewer */}
          <KubeconfigViewer clusterName={clusterName} />

          {/* Instructions */}
          <Instructions />
        </div>
      </main>
    </div>
  );
};

export default Index;
