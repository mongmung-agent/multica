import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@multica/ui/components/ui/card'
import { useSkills } from '../hooks/use-skills'
import { SkillList } from '../components/skill-list'

export default function SkillsPage() {
  const {
    skills,
    loading,
    error,
    toggleSkill,
    refresh,
  } = useSkills()

  return (
    <div className="max-w-4xl mx-auto">
      <Card>
        <CardHeader>
          <CardTitle>Skills</CardTitle>
          <CardDescription>
            Manage agent skills. Skills provide specialized capabilities like Git integration,
            code review, and file manipulation. Toggle skills on/off to control agent behavior.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <SkillList
            skills={skills}
            loading={loading}
            error={error}
            onToggleSkill={toggleSkill}
            onRefresh={refresh}
          />
        </CardContent>
      </Card>
    </div>
  )
}
